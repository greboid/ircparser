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
}

func NewEventBus(ctx context.Context) *EventBus {
	slog.Debug("Creating new event bus", "buffer_size", DefaultEventChannelBuffer)
	ctx, cancel := context.WithCancel(ctx)

	bus := &EventBus{
		handlers: make(map[EventType][]handlerWrapper),
		ctx:      ctx,
		cancel:   cancel,
		eventCh:  make(chan *Event, DefaultEventChannelBuffer),
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

func (eb *EventBus) CountListeners(eventType EventType) int {
	eb.mutex.RLock()
	defer eb.mutex.RUnlock()
	return len(eb.handlers[eventType])
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
	case <-eb.ctx.Done():
	}
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
