package parser

import (
	"time"
)

type testSender struct {
	sentCommands []string
	sentParams   [][]string
	sendError    error
}

func (ts *testSender) Send(command string, params ...string) error {
	ts.sentCommands = append(ts.sentCommands, command)
	ts.sentParams = append(ts.sentParams, params)
	return ts.sendError
}

func (ts *testSender) lastSent() (string, []string) {
	if len(ts.sentCommands) == 0 {
		return "", nil
	}
	idx := len(ts.sentCommands) - 1
	return ts.sentCommands[idx], ts.sentParams[idx]
}

func (ts *testSender) reset() {
	ts.sentCommands = nil
	ts.sentParams = nil
	ts.sendError = nil
}

type testSubscriber struct {
	subscriptions map[EventType][]EventHandler
}

func newTestSubscriber() *testSubscriber {
	return &testSubscriber{
		subscriptions: make(map[EventType][]EventHandler),
	}
}

func (ts *testSubscriber) Subscribe(eventType EventType, handler EventHandler) int {
	ts.subscriptions[eventType] = append(ts.subscriptions[eventType], handler)
	return len(ts.subscriptions[eventType]) - 1
}

func (ts *testSubscriber) emit(event *Event) {
	if handlers, exists := ts.subscriptions[event.Type]; exists {
		for _, handler := range handlers {
			handler(event)
		}
	}
}

type testEmitter struct {
	emittedEvents []*Event
}

func newTestEmitter() *testEmitter {
	return &testEmitter{
		emittedEvents: make([]*Event, 0),
	}
}

func (te *testEmitter) Emit(event *Event) {
	te.emittedEvents = append(te.emittedEvents, event)
}

func (te *testEmitter) lastEmitted() *Event {
	if len(te.emittedEvents) == 0 {
		return nil
	}
	return te.emittedEvents[len(te.emittedEvents)-1]
}

func (te *testEmitter) reset() {
	te.emittedEvents = make([]*Event, 0)
}

// Helper method for tests to simulate EventCapEnd
func TriggerCapEnd(handler *CapabilitiesHandler) {
	handler.HandleCapEnd(&Event{Type: EventCapEnd, Time: time.Now()})
}

// Helper method for tests to signal ready
func SignalCapEndReady(handler *CapabilitiesHandler) {
	handler.HandleCapEndReady(&Event{Type: EventCapEndReady, Time: time.Now()})
}
