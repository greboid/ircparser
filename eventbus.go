package parser

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
)

type EventBus struct {
	handlers map[EventType][]handlerWrapper
	mutex    sync.RWMutex
	ctx      context.Context
	cancel   context.CancelFunc
	eventCh  chan *Event
	wg       sync.WaitGroup
	nextID   int
	idMutex  sync.Mutex

	// Coordination channels for handlers that need to signal completion
	coordinationMux sync.RWMutex
	coordChannels   map[string]chan struct{}
}

func NewEventBus(ctx context.Context) *EventBus {
	slog.Debug("Creating new event bus", "buffer_size", DefaultEventChannelBuffer)
	ctx, cancel := context.WithCancel(ctx)

	bus := &EventBus{
		handlers:      make(map[EventType][]handlerWrapper),
		ctx:           ctx,
		cancel:        cancel,
		eventCh:       make(chan *Event, DefaultEventChannelBuffer),
		coordChannels: make(map[string]chan struct{}),
	}

	bus.wg.Add(1)
	go bus.eventLoop()

	slog.Debug("Event bus created successfully")
	return bus
}

func (eb *EventBus) eventLoop() {
	slog.Debug("Starting event bus loop")
	defer eb.wg.Done()
	defer func() {
		slog.Debug("Event bus loop ended")
	}()

	for {
		select {
		case event := <-eb.eventCh:
			eb.dispatchEvent(event)
		case <-eb.ctx.Done():
			return
		}
	}
}

func (eb *EventBus) dispatchEvent(event *Event) {
	eb.mutex.RLock()
	handlers, exists := eb.handlers[event.Type]
	if !exists {
		eb.mutex.RUnlock()
		return
	}

	handlersCopy := make([]handlerWrapper, len(handlers))
	copy(handlersCopy, handlers)
	eb.mutex.RUnlock()

	slog.Debug("Dispatching to handlers", "event_type", event.Type, "handler_count", len(handlersCopy))

	for _, wrapper := range handlersCopy {
		func() {
			defer func() {
				if r := recover(); r != nil {
					buf := make([]byte, PanicStackBufferSize)
					n := runtime.Stack(buf, false)
					slog.Error("Event handler panic", "event_type", event.Type, "handler_id", wrapper.id, "panic", r, "stack_trace", string(buf[:n]))
				}
			}()
			wrapper.handler(event)
		}()
	}
}

func (eb *EventBus) Subscribe(eventType EventType, handler EventHandler) int {
	eb.idMutex.Lock()
	id := eb.nextID
	eb.nextID++
	eb.idMutex.Unlock()

	eb.mutex.Lock()
	defer eb.mutex.Unlock()

	eb.handlers[eventType] = append(eb.handlers[eventType], handlerWrapper{id: id, handler: handler})
	slog.Debug("Event handler subscribed", "event_type", eventType, "handler_id", id, "total_handlers", len(eb.handlers[eventType]))
	return id
}

func (eb *EventBus) UnsubscribeByID(eventType EventType, id int) {
	eb.mutex.Lock()
	defer eb.mutex.Unlock()

	handlers := eb.handlers[eventType]
	for i := len(handlers) - 1; i >= 0; i-- {
		if handlers[i].id == id {
			eb.handlers[eventType] = append(handlers[:i], handlers[i+1:]...)
			slog.Debug("Event handler unsubscribed", "event_type", eventType, "handler_id", id, "remaining_handlers", len(eb.handlers[eventType]))
			break
		}
	}
}

func (eb *EventBus) Emit(event *Event) {
	select {
	case eb.eventCh <- event:
		// Event successfully queued
	case <-eb.ctx.Done():
		// Context cancelled, drop event silently
	default:
		// Channel is full, log warning and attempt to make space
		slog.Warn("EventBus: channel full, dropping oldest event", "event_type", event.Type)
		select {
		case <-eb.eventCh:
			// Dropped one event, try to send new one
			select {
			case eb.eventCh <- event:
			default:
				// Still can't send, drop this event too
				slog.Warn("EventBus: failed to queue event, dropping", "event_type", event.Type)
			}
		default:
			// Couldn't even drop an event, just discard this one
			slog.Warn("EventBus: failed to queue event, dropping", "event_type", event.Type)
		}
	}
}

func (eb *EventBus) EmitSync(event *Event) {
	slog.Debug("Emitting event synchronously", "event_type", event.Type)
	eb.dispatchEvent(event)
}

func (eb *EventBus) EmitAndWait(event *Event) {
	slog.Debug("Emitting event and waiting for handlers", "event_type", event.Type)

	eb.mutex.RLock()
	handlers, exists := eb.handlers[event.Type]
	if !exists {
		eb.mutex.RUnlock()
		return
	}

	handlersCopy := make([]handlerWrapper, len(handlers))
	copy(handlersCopy, handlers)
	eb.mutex.RUnlock()

	var wg sync.WaitGroup
	wg.Add(len(handlersCopy))

	slog.Debug("Dispatching to handlers and waiting", "event_type", event.Type, "handler_count", len(handlersCopy))

	for _, wrapper := range handlersCopy {
		go func(w handlerWrapper) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					buf := make([]byte, PanicStackBufferSize)
					n := runtime.Stack(buf, false)
					slog.Error("Event handler panic", "event_type", event.Type, "handler_id", w.id, "panic", r, "stack_trace", string(buf[:n]))
				}
			}()
			w.handler(event)
		}(wrapper)
	}

	wg.Wait()
	slog.Debug("All handlers completed", "event_type", event.Type)
}

func (eb *EventBus) Close() {
	slog.Debug("Closing event bus")
	eb.cancel()
	eb.wg.Wait()
	close(eb.eventCh)
	slog.Debug("Event bus closed")
}

func (eb *EventBus) Wait() {
	eb.wg.Wait()
}

// CreateCoordination creates a coordination channel for handlers to signal completion
func (eb *EventBus) CreateCoordination(name string) <-chan struct{} {
	eb.coordinationMux.Lock()
	defer eb.coordinationMux.Unlock()

	ch := make(chan struct{})
	eb.coordChannels[name] = ch
	slog.Debug("Created coordination channel", "name", name)
	return ch
}

// SignalCoordination signals that a handler has completed its work
func (eb *EventBus) SignalCoordination(name string) {
	eb.coordinationMux.Lock()
	defer eb.coordinationMux.Unlock()

	if ch, exists := eb.coordChannels[name]; exists {
		select {
		case ch <- struct{}{}:
			slog.Debug("Coordination signal sent", "name", name)
		default:
			slog.Warn("Coordination channel full, signal dropped", "name", name)
		}
	} else {
		slog.Warn("Coordination channel does not exist", "name", name)
	}
}

// RemoveCoordination removes a coordination channel
func (eb *EventBus) RemoveCoordination(name string) {
	eb.coordinationMux.Lock()
	defer eb.coordinationMux.Unlock()

	if ch, exists := eb.coordChannels[name]; exists {
		close(ch)
		delete(eb.coordChannels, name)
		slog.Debug("Removed coordination channel", "name", name)
	}
}
