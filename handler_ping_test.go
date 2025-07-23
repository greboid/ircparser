package parser

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

func TestNewPingHandler(t *testing.T) {
	ctx := context.Background()
	sender := &testSender{}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	handler := NewPingHandler(ctx, sender.Send, subscriber.Subscribe, emitter.Emit)

	assert.NotNil(t, handler)
	assert.NotNil(t, handler.send)
	assert.NotNil(t, handler.ctx)
	assert.NotNil(t, handler.cancel)
	assert.Zero(t, handler.lastResponse)
	assert.Nil(t, handler.pingTimer)

	// Verify event subscriptions were registered
	assert.Len(t, subscriber.subscriptions[EventPing], 1)
	assert.Len(t, subscriber.subscriptions[EventRawIncoming], 1)
	assert.Len(t, subscriber.subscriptions[EventRegistered], 1)
}

func TestPingHandler_HandleWelcome(t *testing.T) {
	ctx := context.Background()
	sender := &testSender{}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	handler := NewPingHandler(ctx, sender.Send, subscriber.Subscribe, emitter.Emit)

	assert.Nil(t, handler.pingTimer)

	welcomeEvent := &Event{Type: EventRegistered}
	handler.HandleWelcome(welcomeEvent)

	assert.NotNil(t, handler.pingTimer)

	// Calling HandleWelcome again should not create a new timer
	oldTimer := handler.pingTimer
	handler.HandleWelcome(welcomeEvent)
	assert.Equal(t, oldTimer, handler.pingTimer)
}

func TestPingHandler_SendPing(t *testing.T) {
	ctx := context.Background()
	sender := &testSender{}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	handler := NewPingHandler(ctx, sender.Send, subscriber.Subscribe, emitter.Emit)

	handler.SendPing()

	command, params := sender.lastSent()
	assert.Equal(t, "PING", command)
	assert.Len(t, params, 1)
	assert.NotEmptyf(t, params, "Params should exist")

	// Verify no error event was emitted on success
	errorEvent := emitter.lastEmitted()
	assert.Nil(t, errorEvent)
}

func TestPingHandler_SendPing_Error(t *testing.T) {
	ctx := context.Background()
	sender := &testSender{sendError: errors.New("send failed")}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	handler := NewPingHandler(ctx, sender.Send, subscriber.Subscribe, emitter.Emit)

	handler.SendPing()

	command, params := sender.lastSent()
	assert.Equal(t, "PING", command)
	assert.Len(t, params, 1)

	// Verify error event was emitted
	errorEvent := emitter.lastEmitted()
	assert.NotNil(t, errorEvent)
	assert.Equal(t, EventError, errorEvent.Type)

	errorData, ok := errorEvent.Data.(*ErrorData)
	assert.True(t, ok)
	assert.Contains(t, errorData.Message, "Failed to send ping: send failed")
}

func TestPingHandler_HandleRaw(t *testing.T) {
	ctx := context.Background()
	sender := &testSender{}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	handler := NewPingHandler(ctx, sender.Send, subscriber.Subscribe, emitter.Emit)

	beforeTime := time.Now()
	rawEvent := &Event{Type: EventRawIncoming}
	handler.HandleRaw(rawEvent)
	afterTime := time.Now()

	fmt.Println(beforeTime)
	fmt.Println(handler.lastResponse)
	assert.True(t, handler.lastResponse.After(beforeTime))
	assert.True(t, handler.lastResponse.Before(afterTime))
}

func TestPingHandler_HandleRaw_WithTimer(t *testing.T) {
	ctx := context.Background()
	sender := &testSender{}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	handler := NewPingHandler(ctx, sender.Send, subscriber.Subscribe, emitter.Emit)

	// Create timer first
	welcomeEvent := &Event{Type: EventRegistered}
	handler.HandleWelcome(welcomeEvent)

	assert.NotNil(t, handler.pingTimer)

	beforeTime := time.Now()
	rawEvent := &Event{Type: EventRawIncoming}
	handler.HandleRaw(rawEvent)
	afterTime := time.Now()

	assert.True(t, handler.lastResponse.After(beforeTime))
	assert.True(t, handler.lastResponse.Before(afterTime))
	assert.NotNil(t, handler.pingTimer)
}

func TestPingHandler_HandleRaw_NilTimer(t *testing.T) {
	ctx := context.Background()
	sender := &testSender{}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	handler := NewPingHandler(ctx, sender.Send, subscriber.Subscribe, emitter.Emit)

	assert.Nil(t, handler.pingTimer)

	beforeTime := time.Now()
	rawEvent := &Event{Type: EventRawIncoming}
	handler.HandleRaw(rawEvent)
	afterTime := time.Now()

	assert.True(t, handler.lastResponse.After(beforeTime))
	assert.True(t, handler.lastResponse.Before(afterTime))
	assert.Nil(t, handler.pingTimer)
}

func TestPingHandler_HandlePing(t *testing.T) {
	ctx := context.Background()
	sender := &testSender{}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	handler := NewPingHandler(ctx, sender.Send, subscriber.Subscribe, emitter.Emit)

	serverName := "irc.example.com"
	pingData := &PingData{Server: serverName}
	pingEvent := &Event{
		Type: EventPing,
		Data: pingData,
	}

	handler.HandlePing(pingEvent)

	command, params := sender.lastSent()
	assert.Equal(t, "PONG", command)
	assert.Equal(t, []string{serverName}, params)

	// Verify no error event was emitted on success
	errorEvent := emitter.lastEmitted()
	assert.Nil(t, errorEvent)
}

func TestPingHandler_HandlePing_Error(t *testing.T) {
	ctx := context.Background()
	sender := &testSender{sendError: errors.New("pong failed")}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	handler := NewPingHandler(ctx, sender.Send, subscriber.Subscribe, emitter.Emit)

	serverName := "irc.example.com"
	pingData := &PingData{Server: serverName}
	pingEvent := &Event{
		Type: EventPing,
		Data: pingData,
	}

	// Should not panic on send error
	handler.HandlePing(pingEvent)

	command, params := sender.lastSent()
	assert.Equal(t, "PONG", command)
	assert.Equal(t, []string{serverName}, params)

	// Verify error event was emitted
	errorEvent := emitter.lastEmitted()
	assert.NotNil(t, errorEvent)
	assert.Equal(t, EventError, errorEvent.Type)

	errorData, ok := errorEvent.Data.(*ErrorData)
	assert.True(t, ok)
	assert.Contains(t, errorData.Message, "Failed to send ping: pong failed")
}

func TestPingHandler_HandlePing_InvalidData(t *testing.T) {
	ctx := context.Background()
	sender := &testSender{}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	handler := NewPingHandler(ctx, sender.Send, subscriber.Subscribe, emitter.Emit)

	invalidEvent := &Event{
		Type: EventPing,
		Data: "invalid data type",
	}

	handler.HandlePing(invalidEvent)

	command, _ := sender.lastSent()
	assert.Empty(t, command)
}

func TestPingHandler_HandlePing_NilData(t *testing.T) {
	ctx := context.Background()
	sender := &testSender{}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	handler := NewPingHandler(ctx, sender.Send, subscriber.Subscribe, emitter.Emit)

	nilDataEvent := &Event{
		Type: EventPing,
		Data: nil,
	}

	handler.HandlePing(nilDataEvent)

	command, _ := sender.lastSent()
	assert.Empty(t, command)
}

func TestPingHandler_Integration_EventSubscriptions(t *testing.T) {
	ctx := context.Background()
	sender := &testSender{}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	handler := NewPingHandler(ctx, sender.Send, subscriber.Subscribe, emitter.Emit)

	// Test ping event handling
	serverName := "irc.example.com"
	pingData := &PingData{Server: serverName}
	pingEvent := &Event{
		Type: EventPing,
		Data: pingData,
	}

	subscriber.emit(pingEvent)

	command, params := sender.lastSent()
	assert.Equal(t, "PONG", command)
	assert.Equal(t, []string{serverName}, params)

	// Test raw event handling
	rawEvent := &Event{Type: EventRawIncoming}
	beforeTime := time.Now()
	subscriber.emit(rawEvent)
	afterTime := time.Now()

	assert.True(t, handler.lastResponse.After(beforeTime))
	assert.True(t, handler.lastResponse.Before(afterTime))

	// Test welcome event handling
	welcomeEvent := &Event{Type: EventRegistered}
	subscriber.emit(welcomeEvent)

	assert.NotNil(t, handler.pingTimer)
}

func TestPingHandler_Context_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sender := &testSender{}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	handler := NewPingHandler(ctx, sender.Send, subscriber.Subscribe, emitter.Emit)

	assert.NotNil(t, handler.cancel)

	cancel()

	select {
	case <-handler.ctx.Done():
		// Success - context was cancelled
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Handler context should be cancelled")
	}
}

func TestPingHandler_Timer_Reset(t *testing.T) {
	ctx := context.Background()
	sender := &testSender{}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	handler := NewPingHandler(ctx, sender.Send, subscriber.Subscribe, emitter.Emit)

	// Initialize timer
	welcomeEvent := &Event{Type: EventRegistered}
	handler.HandleWelcome(welcomeEvent)
	assert.NotNil(t, handler.pingTimer)

	// Store original timer
	originalTimer := handler.pingTimer

	// Handle raw event which should reset the timer
	rawEvent := &Event{Type: EventRawIncoming}
	handler.HandleRaw(rawEvent)

	// Timer should still exist (same object, but reset)
	assert.NotNil(t, handler.pingTimer)
	assert.Equal(t, originalTimer, handler.pingTimer)
}

func TestPingHandler_Send_Function_Called_Correctly(t *testing.T) {
	ctx := context.Background()
	sender := &testSender{}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	handler := NewPingHandler(ctx, sender.Send, subscriber.Subscribe, emitter.Emit)

	// Test SendPing calls send function
	handler.SendPing()
	command, params := sender.lastSent()
	assert.Equal(t, "PING", command)
	assert.Len(t, params, 1)

	// Reset sender
	sender.reset()

	// Test HandlePing calls send function
	serverName := "test.server.com"
	pingData := &PingData{Server: serverName}
	pingEvent := &Event{Type: EventPing, Data: pingData}
	handler.HandlePing(pingEvent)

	command, params = sender.lastSent()
	assert.Equal(t, "PONG", command)
	assert.Equal(t, []string{serverName}, params)
}
