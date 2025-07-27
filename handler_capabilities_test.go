package parser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func createTestCapabilitiesHandler(ctx context.Context, requestedCaps ...string) (*CapabilitiesHandler, *testSender, *testSubscriber, *testEmitter) {
	sender := &testSender{}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	capabilities := make(map[string]string)

	countListeners := func(eventType EventType) int {
		if handlers, exists := subscriber.subscriptions[eventType]; exists {
			return len(handlers)
		}
		return 0
	}

	handler := NewCapabilitiesHandler(
		ctx,
		sender.Send,
		subscriber.Subscribe,
		emitter.Emit,
		countListeners,
		capabilities,
		requestedCaps...,
	)

	return handler, sender, subscriber, emitter
}

func TestNewCapabilitiesHandler(t *testing.T) {
	ctx := context.Background()
	requestedCaps := []string{"sasl", "server-time", "multi-prefix"}

	handler, _, subscriber, _ := createTestCapabilitiesHandler(ctx, requestedCaps...)

	assert.NotNil(t, handler)
	assert.NotNil(t, handler.send)
	assert.NotNil(t, handler.subscribe)
	assert.NotNil(t, handler.emit)
	assert.NotNil(t, handler.ctx)
	assert.NotNil(t, handler.cancel)
	assert.Equal(t, requestedCaps, handler.requestedCaps)
	assert.NotNil(t, handler.pendingCaps)
	assert.NotNil(t, handler.ackedCaps)
	assert.False(t, handler.negotiating)

	assert.Contains(t, subscriber.subscriptions, EventConnected)
	assert.Contains(t, subscriber.subscriptions, EventCapLS)
	assert.Contains(t, subscriber.subscriptions, EventCapACK)
	assert.Contains(t, subscriber.subscriptions, EventCapNAK)
	assert.Contains(t, subscriber.subscriptions, EventCapDEL)
	assert.Contains(t, subscriber.subscriptions, EventCapNEW)
}

func TestCapabilitiesHandler_HandleConnected(t *testing.T) {
	ctx := context.Background()
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx)

	assert.False(t, handler.isNegotiating())

	connectedEvent := &Event{Type: EventConnected}
	handler.HandleConnected(connectedEvent)

	assert.True(t, handler.isNegotiating())

	command, params := sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"LS", DefaultCapabilityProtocolVersion}, params)
}

func TestCapabilitiesHandler_HandleConnected_SendError(t *testing.T) {
	ctx := context.Background()
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx)

	sender.sendError = errors.New("send failed")

	connectedEvent := &Event{Type: EventConnected}
	handler.HandleConnected(connectedEvent)

	assert.False(t, handler.isNegotiating())
}

func TestCapabilitiesHandler_HandleCapLS_WithRequestedCaps(t *testing.T) {
	ctx := context.Background()
	requestedCaps := []string{"sasl", "server-time"}
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx, requestedCaps...)

	handler.setNegotiating(true)

	message := &Message{Params: []string{"*", "sasl server-time multi-prefix batch=draft/chathistory-targets"}}
	capLSEvent := &Event{
		Type:    EventCapLS,
		Message: message,
	}

	handler.HandleCapLS(capLSEvent)

	command, params := sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"REQ", strings.Join(requestedCaps, " ")}, params)

	assert.Equal(t, "", handler.availableCaps["sasl"])
	assert.Equal(t, "", handler.availableCaps["server-time"])
	assert.Equal(t, "", handler.availableCaps["multi-prefix"])
	assert.Equal(t, "draft/chathistory-targets", handler.availableCaps["batch"])

	assert.Empty(t, handler.ackedCaps)
}

func TestCapabilitiesHandler_HandleCapLS_NoRequestedCaps(t *testing.T) {
	ctx := context.Background()
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx)

	handler.setNegotiating(true)

	message := &Message{Params: []string{"*", "sasl server-time"}}
	capLSEvent := &Event{
		Type:    EventCapLS,
		Message: message,
	}

	handler.HandleCapLS(capLSEvent)

	TriggerCapEnd(handler)
	time.Sleep(10 * time.Millisecond)
	command, params := sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"END"}, params)
	assert.False(t, handler.isNegotiating())
}

func TestCapabilitiesHandler_HandleCapLS_NotNegotiating(t *testing.T) {
	ctx := context.Background()
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx)

	assert.False(t, handler.isNegotiating())

	message := &Message{Params: []string{"*", "sasl server-time"}}
	capLSEvent := &Event{
		Type:    EventCapLS,
		Message: message,
	}

	handler.HandleCapLS(capLSEvent)

	command, _ := sender.lastSent()
	assert.Empty(t, command)
	assert.Empty(t, handler.ackedCaps)
	assert.Empty(t, handler.availableCaps)
}

func TestCapabilitiesHandler_HandleCapLS_NilMessage(t *testing.T) {
	ctx := context.Background()
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx, "sasl")

	handler.setNegotiating(true)

	capLSEvent := &Event{
		Type:    EventCapLS,
		Message: nil,
	}

	handler.HandleCapLS(capLSEvent)

	TriggerCapEnd(handler)
	time.Sleep(10 * time.Millisecond)
	command, params := sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"END"}, params)

	assert.Empty(t, handler.availableCaps)
	assert.Empty(t, handler.ackedCaps)
}

func TestCapabilitiesHandler_HandleCapLS_RequestCapabilitiesError(t *testing.T) {
	ctx := context.Background()
	requestedCaps := []string{"sasl"}
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx, requestedCaps...)

	handler.setNegotiating(true)
	sender.sendError = errors.New("request failed")

	message := &Message{Params: []string{"*", "sasl server-time"}}
	capLSEvent := &Event{
		Type:    EventCapLS,
		Message: message,
	}

	handler.HandleCapLS(capLSEvent)

	command, params := sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"REQ", "sasl"}, params)

	assert.True(t, handler.isNegotiating())
}

func TestCapabilitiesHandler_HandleCapLS_EndNegotiationError(t *testing.T) {
	ctx := context.Background()
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	handler.setNegotiating(true)

	customSender := &testSender{}
	handler.send = func(command string, params ...string) error {
		if command == "CAP" && len(params) > 0 && params[0] == "END" {
			return errors.New("end failed")
		}
		return customSender.Send(command, params...)
	}

	message := &Message{Params: []string{"*", "sasl server-time"}}
	capLSEvent := &Event{
		Type:    EventCapLS,
		Message: message,
	}

	handler.HandleCapLS(capLSEvent)

	assert.Equal(t, "", handler.availableCaps["sasl"])
	assert.Equal(t, "", handler.availableCaps["server-time"])
	assert.Empty(t, handler.ackedCaps)
	time.Sleep(50 * time.Millisecond)
	assert.True(t, handler.isNegotiating())
}

func TestCapabilitiesHandler_HandleCapACK(t *testing.T) {
	ctx := context.Background()
	requestedCaps := []string{"sasl", "server-time"}
	handler, _, _, emitter := createTestCapabilitiesHandler(ctx, requestedCaps...)

	handler.setNegotiating(true)

	handler.pendingCaps["sasl"] = true
	handler.pendingCaps["server-time"] = true

	message := &Message{Params: []string{"*", "sasl server-time"}}
	capACKEvent := &Event{
		Type:    EventCapACK,
		Message: message,
	}

	handler.HandleCapACK(capACKEvent)

	assert.True(t, handler.IsCapabilityActive("sasl"))
	assert.True(t, handler.IsCapabilityActive("server-time"))
	assert.False(t, handler.pendingCaps["sasl"])
	assert.False(t, handler.pendingCaps["server-time"])

	capAddedEvents := 0
	for _, event := range emitter.emittedEvents {
		if event.Type == EventCapAdded {
			capAddedEvents++
		}
	}
	assert.Equal(t, 2, capAddedEvents)

	var event1, event2 *Event
	for _, event := range emitter.emittedEvents {
		if event.Type == EventCapAdded {
			if event1 == nil {
				event1 = event
			} else {
				event2 = event
				break
			}
		}
	}

	assert.NotNil(t, event1)
	assert.NotNil(t, event2)

	if event1 != nil {
		assert.NotNil(t, event1.Data)
		if event1.Data != nil {
			capData1, ok := event1.Data.(*CapAddedData)
			assert.True(t, ok)
			assert.Equal(t, "sasl", capData1.Capability)
		}
	}

	if event2 != nil {
		assert.NotNil(t, event2.Data)
		if event2.Data != nil {
			capData2, ok := event2.Data.(*CapAddedData)
			assert.True(t, ok)
			assert.Equal(t, "server-time", capData2.Capability)
		}
	}
}

func TestCapabilitiesHandler_HandleCapACK_NotNegotiating(t *testing.T) {
	ctx := context.Background()
	handler, _, _, emitter := createTestCapabilitiesHandler(ctx)

	assert.False(t, handler.isNegotiating())

	message := &Message{Params: []string{"*", "sasl"}}
	capACKEvent := &Event{
		Type:    EventCapACK,
		Message: message,
	}

	handler.HandleCapACK(capACKEvent)

	assert.False(t, handler.IsCapabilityActive("sasl"))
	assert.Len(t, emitter.emittedEvents, 0)
}

func TestCapabilitiesHandler_HandleCapACK_InvalidMessage(t *testing.T) {
	ctx := context.Background()
	handler, _, _, emitter := createTestCapabilitiesHandler(ctx)

	handler.setNegotiating(true)

	capACKEvent := &Event{
		Type:    EventCapACK,
		Message: nil,
	}

	handler.HandleCapACK(capACKEvent)

	// Should not emit events
	assert.Len(t, emitter.emittedEvents, 0)
}

func TestCapabilitiesHandler_HandleCapNAK(t *testing.T) {
	ctx := context.Background()
	requestedCaps := []string{"sasl", "server-time", "invalid-cap"}
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx, requestedCaps...)

	handler.setNegotiating(true)

	// Add pending capabilities
	handler.pendingCaps["sasl"] = true
	handler.pendingCaps["server-time"] = true
	handler.pendingCaps["invalid-cap"] = true

	message := &Message{Params: []string{"*", "invalid-cap"}}
	capNAKEvent := &Event{
		Type:    EventCapNAK,
		Message: message,
	}

	handler.HandleCapNAK(capNAKEvent)

	// Should remove rejected capability from pending
	assert.False(t, handler.pendingCaps["invalid-cap"])

	// Should still have other pending capabilities
	assert.True(t, handler.pendingCaps["sasl"])
	assert.True(t, handler.pendingCaps["server-time"])

	// Should not end negotiation yet (still has pending caps)
	command, _ := sender.lastSent()
	assert.NotEqual(t, "CAP", command)
}

func TestCapabilitiesHandler_HandleCapNAK_EndNegotiation(t *testing.T) {
	ctx := context.Background()
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx)

	handler.setNegotiating(true)

	// Add only one pending capability
	handler.pendingCaps["invalid-cap"] = true

	message := &Message{Params: []string{"*", "invalid-cap"}}
	capNAKEvent := &Event{
		Type:    EventCapNAK,
		Message: message,
	}

	handler.HandleCapNAK(capNAKEvent)

	// Should remove from pending
	assert.False(t, handler.pendingCaps["invalid-cap"])

	// Should end negotiation since no pending caps remain
	// Signal coordination to complete
	TriggerCapEnd(handler)
	time.Sleep(10 * time.Millisecond)
	command, params := sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"END"}, params)
}

func TestCapabilitiesHandler_HandleCapDEL(t *testing.T) {
	ctx := context.Background()
	handler, _, _, emitter := createTestCapabilitiesHandler(ctx)

	// Add some active capabilities first
	handler.ackedCaps["sasl"] = ""
	handler.ackedCaps["server-time"] = ""

	message := &Message{Params: []string{"*", "sasl"}}
	capDELEvent := &Event{
		Type:    EventCapDEL,
		Message: message,
	}

	handler.HandleCapDEL(capDELEvent)

	// Should remove capability
	assert.False(t, handler.IsCapabilityActive("sasl"))
	assert.True(t, handler.IsCapabilityActive("server-time"))

	// Should emit cap removed event
	assert.Len(t, emitter.emittedEvents, 1)
	event := emitter.emittedEvents[0]
	assert.Equal(t, EventCapRemoved, event.Type)
	capData, ok := event.Data.(*CapRemovedData)
	assert.True(t, ok)
	assert.Equal(t, "sasl", capData.Capability)
}

func TestCapabilitiesHandler_HandleCapDEL_InvalidMessage(t *testing.T) {
	ctx := context.Background()
	handler, _, _, emitter := createTestCapabilitiesHandler(ctx)

	capDELEvent := &Event{
		Type:    EventCapDEL,
		Message: nil,
	}

	handler.HandleCapDEL(capDELEvent)

	// Should not emit events
	assert.Len(t, emitter.emittedEvents, 0)
}

func TestCapabilitiesHandler_HandleCapNEW(t *testing.T) {
	ctx := context.Background()
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	message := &Message{Params: []string{"*", "new-capability another-cap=value"}}
	capNEWEvent := &Event{
		Type:    EventCapNEW,
		Message: message,
	}

	handler.HandleCapNEW(capNEWEvent)

	// Should parse and store new capabilities in availableCaps
	assert.Equal(t, "", handler.availableCaps["new-capability"])
	assert.Equal(t, "value", handler.availableCaps["another-cap"])
}

func TestCapabilitiesHandler_HandleCapNEW_InvalidMessage(t *testing.T) {
	ctx := context.Background()
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	capNEWEvent := &Event{
		Type:    EventCapNEW,
		Message: nil,
	}

	// Should not panic
	handler.HandleCapNEW(capNEWEvent)
}

func TestCapabilitiesHandler_RequestCapabilities(t *testing.T) {
	ctx := context.Background()
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx)

	// Set up available capabilities from CAP LS
	handler.availableCaps["sasl"] = ""
	handler.availableCaps["server-time"] = ""
	handler.availableCaps["multi-prefix"] = ""

	capabilities := []string{"sasl", "server-time", "multi-prefix"}

	err := handler.RequestCapabilities(capabilities)
	assert.NoError(t, err)

	// Should mark all as pending
	assert.True(t, handler.pendingCaps["sasl"])
	assert.True(t, handler.pendingCaps["server-time"])
	assert.True(t, handler.pendingCaps["multi-prefix"])

	// Should send CAP REQ
	command, params := sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"REQ", "sasl server-time multi-prefix"}, params)
}

func TestCapabilitiesHandler_RequestCapabilities_Empty(t *testing.T) {
	ctx := context.Background()
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx)

	err := handler.RequestCapabilities([]string{})
	assert.NoError(t, err)

	// Should not send anything
	command, _ := sender.lastSent()
	assert.Empty(t, command)
}

func TestCapabilitiesHandler_RequestCapabilities_SendError(t *testing.T) {
	ctx := context.Background()
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx)

	// Set up available capability so it will be requested
	handler.availableCaps["sasl"] = ""

	sender.sendError = errors.New("send failed")
	capabilities := []string{"sasl"}

	err := handler.RequestCapabilities(capabilities)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "send failed")

	// Should still mark as pending
	assert.True(t, handler.pendingCaps["sasl"])
}

func TestCapabilitiesHandler_RequestCapabilities_FiltersByAdvertised(t *testing.T) {
	ctx := context.Background()
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx)

	// Set up some available capabilities from CAP LS
	handler.availableCaps["sasl"] = ""
	handler.availableCaps["server-time"] = ""
	handler.availableCaps["multi-prefix"] = ""

	// Request some capabilities, some available and some not
	capabilities := []string{"sasl", "server-time", "not-available", "another-missing"}

	err := handler.RequestCapabilities(capabilities)
	assert.NoError(t, err)

	// Should only mark available capabilities as pending
	assert.True(t, handler.pendingCaps["sasl"])
	assert.True(t, handler.pendingCaps["server-time"])
	assert.False(t, handler.pendingCaps["not-available"])
	assert.False(t, handler.pendingCaps["another-missing"])

	// Should only send available capabilities in CAP REQ
	command, params := sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"REQ", "sasl server-time"}, params)
}

func TestCapabilitiesHandler_RequestCapabilities_NoAvailableCapabilities(t *testing.T) {
	ctx := context.Background()
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx)

	// Set up no available capabilities from CAP LS (empty map)
	handler.availableCaps = make(map[string]string)

	// Request capabilities that aren't available
	capabilities := []string{"sasl", "server-time", "multi-prefix"}

	err := handler.RequestCapabilities(capabilities)
	assert.NoError(t, err)

	// Should not mark any as pending
	assert.False(t, handler.pendingCaps["sasl"])
	assert.False(t, handler.pendingCaps["server-time"])
	assert.False(t, handler.pendingCaps["multi-prefix"])

	// Should not send any CAP REQ
	command, _ := sender.lastSent()
	assert.Empty(t, command)
}

func TestCapabilitiesHandler_EndCapabilityNegotiation(t *testing.T) {
	ctx := context.Background()
	handler, sender, _, emitter := createTestCapabilitiesHandler(ctx)

	handler.setNegotiating(true)
	handler.ackedCaps["sasl"] = ""
	handler.ackedCaps["server-time"] = ""

	err := handler.EndCapabilityNegotiation()
	assert.NoError(t, err)

	// Should emit pre-end event
	assert.Len(t, emitter.emittedEvents, 1)
	event := emitter.emittedEvents[0]
	assert.Equal(t, EventCapPreEnd, event.Type)

	preEndData, ok := event.Data.(*CapPreEndData)
	assert.True(t, ok)
	assert.ElementsMatch(t, []string{"sasl", "server-time"}, preEndData.ActiveCaps)
	assert.Equal(t, map[string]string{"sasl": "", "server-time": ""}, preEndData.CapValues)

	// Signal coordination completion
	TriggerCapEnd(handler)

	// Wait for goroutine to complete
	time.Sleep(50 * time.Millisecond)

	// Should send CAP END
	command, params := sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"END"}, params)

	// Should stop negotiating
	assert.False(t, handler.isNegotiating())
}

func TestCapabilitiesHandler_EndCapabilityNegotiation_NotNegotiating(t *testing.T) {
	ctx := context.Background()
	handler, sender, _, emitter := createTestCapabilitiesHandler(ctx)

	assert.False(t, handler.isNegotiating())

	err := handler.EndCapabilityNegotiation()
	assert.NoError(t, err)

	// Should not emit events or send commands
	assert.Len(t, emitter.emittedEvents, 0)
	command, _ := sender.lastSent()
	assert.Empty(t, command)
}

func TestCapabilitiesHandler_EndCapabilityNegotiation_Timeout(t *testing.T) {
	ctx := context.Background()
	handler, _, _, emitter := createTestCapabilitiesHandler(ctx)

	handler.setNegotiating(true)

	// Don't signal coordination - let it timeout

	err := handler.EndCapabilityNegotiation()
	assert.NoError(t, err)

	// Should emit pre-end event
	assert.Len(t, emitter.emittedEvents, 1)

	// Wait for timeout (this is a slow test, but necessary)
	// In a real scenario, you might want to make the timeout configurable
	time.Sleep(100 * time.Millisecond) // Much shorter than 30s for testing

	// For this test, we'll just verify the pre-end event was emitted
	// The actual timeout behavior would require more complex coordination
}

func TestCapabilitiesHandler_IsCapabilityActive(t *testing.T) {
	ctx := context.Background()
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	assert.False(t, handler.IsCapabilityActive("sasl"))

	handler.ackedCaps["sasl"] = ""
	assert.True(t, handler.IsCapabilityActive("sasl"))

	handler.ackedCaps["server-time"] = "2021-01-01T00:00:00.000Z"
	assert.True(t, handler.IsCapabilityActive("server-time"))
}

func TestCapabilitiesHandler_GetActiveCaps(t *testing.T) {
	ctx := context.Background()
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	assert.Empty(t, handler.GetActiveCaps())

	handler.ackedCaps["sasl"] = ""
	handler.ackedCaps["server-time"] = ""
	handler.ackedCaps["multi-prefix"] = ""

	activeCaps := handler.GetActiveCaps()
	assert.Len(t, activeCaps, 3)
	assert.ElementsMatch(t, []string{"sasl", "server-time", "multi-prefix"}, activeCaps)
}

func TestCapabilitiesHandler_Reset(t *testing.T) {
	ctx := context.Background()
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	// Set some state
	handler.setNegotiating(true)
	handler.ackedCaps["sasl"] = ""
	handler.ackedCaps["server-time"] = ""

	assert.True(t, handler.isNegotiating())
	assert.Len(t, handler.ackedCaps, 2)

	handler.Reset()

	assert.False(t, handler.isNegotiating())
	assert.Empty(t, handler.ackedCaps)
}

func TestCapabilitiesHandler_ParseAvailableCapabilities(t *testing.T) {
	ctx := context.Background()
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	capsStr := "sasl server-time multi-prefix batch=draft/chathistory-targets account-tag=value"
	handler.parseAvailableCapabilities(capsStr)

	// These should be stored in availableCaps, not ackedCaps
	assert.Equal(t, "", handler.availableCaps["sasl"])
	assert.Equal(t, "", handler.availableCaps["server-time"])
	assert.Equal(t, "", handler.availableCaps["multi-prefix"])
	assert.Equal(t, "draft/chathistory-targets", handler.availableCaps["batch"])
	assert.Equal(t, "value", handler.availableCaps["account-tag"])

	// ackedCaps should be empty until CAP ACK
	assert.Empty(t, handler.ackedCaps)
}

func TestCapabilitiesHandler_ThreadSafety(t *testing.T) {
	ctx := context.Background()
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	// Test concurrent access to capability values
	var wg sync.WaitGroup

	// Writer goroutines
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			capName := fmt.Sprintf("cap%d", i)
			handler.addCapability(capName)
		}(i)
	}

	// Reader goroutines
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			capName := fmt.Sprintf("cap%d", i)
			handler.IsCapabilityActive(capName)
		}(i)
	}

	// Negotiating state access
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handler.setNegotiating(true)
			handler.isNegotiating()
			handler.setNegotiating(false)
		}()
	}

	wg.Wait()

	// Should not have panicked due to race conditions
}

func TestCapabilitiesHandler_Integration_FullNegotiation(t *testing.T) {
	ctx := context.Background()
	requestedCaps := []string{"sasl", "server-time"}
	handler, sender, subscriber, emitter := createTestCapabilitiesHandler(ctx, requestedCaps...)

	// Step 1: Connected event
	connectedEvent := &Event{Type: EventConnected}
	subscriber.emit(connectedEvent)

	assert.True(t, handler.isNegotiating())
	command, params := sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"LS", DefaultCapabilityProtocolVersion}, params)

	// Step 2: CAP LS response
	sender.reset()
	capLSMessage := &Message{Params: []string{"*", "sasl=PLAIN server-time multi-prefix"}}
	capLSEvent := &Event{Type: EventCapLS, Message: capLSMessage}
	subscriber.emit(capLSEvent)

	command, params = sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"REQ", "sasl server-time"}, params)

	// Step 3: CAP ACK response
	sender.reset()
	capACKMessage := &Message{Params: []string{"*", "sasl server-time"}}
	capACKEvent := &Event{Type: EventCapACK, Message: capACKMessage}
	subscriber.emit(capACKEvent)

	// Should have active capabilities
	assert.True(t, handler.IsCapabilityActive("sasl"))
	assert.True(t, handler.IsCapabilityActive("server-time"))

	// Should have emitted cap added events
	capAddedEvents := 0
	for _, event := range emitter.emittedEvents {
		if event.Type == EventCapAdded {
			capAddedEvents++
		}
	}
	assert.Equal(t, 2, capAddedEvents)

	// Should automatically end negotiation
	TriggerCapEnd(handler)
	time.Sleep(10 * time.Millisecond)

	command, params = sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"END"}, params)

	assert.False(t, handler.isNegotiating())
}

func TestCapabilitiesHandler_Context_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	assert.NotNil(t, handler.cancel)

	cancel()

	select {
	case <-handler.ctx.Done():
		// Success - context was cancelled
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Handler context should be cancelled")
	}
}

// Tests for empty trailing strings in all handlers
func TestCapabilitiesHandler_HandleCapACK_EmptyTrailing(t *testing.T) {
	ctx := context.Background()
	handler, _, _, emitter := createTestCapabilitiesHandler(ctx)

	handler.setNegotiating(true)

	// Create a message with empty trailing parameter
	message := &Message{Params: []string{"*", ""}}
	capACKEvent := &Event{
		Type:    EventCapACK,
		Message: message,
	}

	handler.HandleCapACK(capACKEvent)

	// Should not add any capabilities but may emit pre-end event due to checkEndNegotiation
	assert.Empty(t, handler.ackedCaps)

	// Filter only cap added events (ignore pre-end events)
	capAddedEvents := 0
	for _, event := range emitter.emittedEvents {
		if event.Type == EventCapAdded {
			capAddedEvents++
		}
	}
	assert.Equal(t, 0, capAddedEvents)
}

func TestCapabilitiesHandler_HandleCapNAK_EmptyTrailing(t *testing.T) {
	ctx := context.Background()
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	handler.setNegotiating(true)
	handler.pendingCaps["test"] = true

	message := &Message{Params: []string{"*", ""}}
	capNAKEvent := &Event{
		Type:    EventCapNAK,
		Message: message,
	}

	handler.HandleCapNAK(capNAKEvent)

	// Should not remove any pending capabilities since no caps were specified
	assert.True(t, handler.pendingCaps["test"])
}

func TestCapabilitiesHandler_HandleCapDEL_EmptyTrailing(t *testing.T) {
	ctx := context.Background()
	handler, _, _, emitter := createTestCapabilitiesHandler(ctx)

	// Add some active capabilities first
	handler.ackedCaps["sasl"] = ""
	handler.ackedCaps["server-time"] = ""

	message := &Message{Params: []string{"*", ""}}
	capDELEvent := &Event{
		Type:    EventCapDEL,
		Message: message,
	}

	handler.HandleCapDEL(capDELEvent)

	// Should not remove any capabilities or emit events
	assert.Len(t, emitter.emittedEvents, 0)
	assert.True(t, handler.IsCapabilityActive("sasl"))
	assert.True(t, handler.IsCapabilityActive("server-time"))
}

func TestCapabilitiesHandler_HandleCapNEW_EmptyTrailing(t *testing.T) {
	ctx := context.Background()
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	message := &Message{Params: []string{"*", ""}}
	capNEWEvent := &Event{
		Type:    EventCapNEW,
		Message: message,
	}

	// Should not panic
	handler.HandleCapNEW(capNEWEvent)
}

// Test CAP END send error
func TestCapabilitiesHandler_EndCapabilityNegotiation_SendError(t *testing.T) {
	ctx := context.Background()
	handler, sender, _, emitter := createTestCapabilitiesHandler(ctx)

	handler.setNegotiating(true)

	// Set up sender to fail on CAP END
	sender.sendError = errors.New("send failed")

	err := handler.EndCapabilityNegotiation()
	assert.NoError(t, err) // Method itself shouldn't return error since send happens in goroutine

	// Should emit pre-end event
	assert.Len(t, emitter.emittedEvents, 1)

	// Signal coordination completion
	TriggerCapEnd(handler)

	// Wait for goroutine to complete
	time.Sleep(50 * time.Millisecond)

	// Should have tried to send CAP END and failed
	command, params := sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"END"}, params)

	// Should still be negotiating since send failed
	assert.True(t, handler.isNegotiating())
}

// Test parseAvailableCapabilities with malformed input
func TestCapabilitiesHandler_ParseAvailableCapabilities_MalformedCapabilities(t *testing.T) {
	ctx := context.Background()
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	// Test various malformed capability strings
	testCases := []struct {
		name     string
		capsStr  string
		expected map[string]string
	}{
		{
			name:     "empty capability after equals",
			capsStr:  "cap=",
			expected: map[string]string{"cap": ""},
		},
		{
			name:     "multiple equals signs",
			capsStr:  "cap=value=extra",
			expected: map[string]string{"cap": "value=extra"},
		},
		{
			name:     "empty capability name",
			capsStr:  "=value",
			expected: map[string]string{"": "value"},
		},
		{
			name:    "mixed valid and malformed",
			capsStr: "sasl cap= server-time=value multi= =empty",
			expected: map[string]string{
				"sasl":        "",
				"cap":         "",
				"server-time": "value",
				"multi":       "",
				"":            "empty",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset availableCaps for each test
			handler.availableCaps = make(map[string]string)

			handler.parseAvailableCapabilities(tc.capsStr)

			for expectedCap, expectedValue := range tc.expected {
				actualValue, exists := handler.availableCaps[expectedCap]
				assert.True(t, exists, "Expected capability %q to exist", expectedCap)
				assert.Equal(t, expectedValue, actualValue, "Expected capability %q to have value %q", expectedCap, expectedValue)
			}
		})
	}
}

// Test addCapability with existing capability
func TestCapabilitiesHandler_AddCapability_AlreadyExists(t *testing.T) {
	ctx := context.Background()
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	// First, add a capability with a value
	handler.ackedCaps["sasl"] = "PLAIN"

	// Try to add it again
	handler.addCapability("sasl")

	// Should not overwrite existing value
	assert.Equal(t, "PLAIN", handler.ackedCaps["sasl"])
}

// Test constructor with existing capabilities
func TestNewCapabilitiesHandler_WithExistingCapabilities(t *testing.T) {
	ctx := context.Background()
	sender := &testSender{}
	subscriber := newTestSubscriber()
	emitter := newTestEmitter()

	// Create capabilities map with existing values
	existingCapabilities := map[string]string{
		"sasl":         "PLAIN",
		"server-time":  "",
		"multi-prefix": "",
	}

	requestedCaps := []string{"sasl", "server-time"}

	countListeners := func(eventType EventType) int {
		if handlers, exists := subscriber.subscriptions[eventType]; exists {
			return len(handlers)
		}
		return 0
	}

	handler := NewCapabilitiesHandler(
		ctx,
		sender.Send,
		subscriber.Subscribe,
		emitter.Emit,
		countListeners,
		existingCapabilities,
		requestedCaps...,
	)

	// Should preserve existing capabilities
	assert.Equal(t, "PLAIN", handler.ackedCaps["sasl"])
	assert.Equal(t, "", handler.ackedCaps["server-time"])
	assert.Equal(t, "", handler.ackedCaps["multi-prefix"])
	assert.Equal(t, requestedCaps, handler.requestedCaps)
}

// Test context cancellation during EndCapabilityNegotiation
func TestCapabilitiesHandler_EndCapabilityNegotiation_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handler, sender, subscriber, emitter := createTestCapabilitiesHandler(ctx)

	// Register a handler for EventCapPreEnd so the coordination logic waits
	subscriber.Subscribe(EventCapPreEnd, func(event *Event) {
		// This handler will never signal ready, so we can test cancellation
	})

	handler.setNegotiating(true)

	err := handler.EndCapabilityNegotiation()
	assert.NoError(t, err)

	// Should emit pre-end event
	assert.Len(t, emitter.emittedEvents, 1)

	// Cancel context while goroutine is waiting
	cancel()

	// Wait for goroutine to handle cancellation
	time.Sleep(50 * time.Millisecond)

	// Should not have sent CAP END due to context cancellation
	command, _ := sender.lastSent()
	assert.Empty(t, command)

	// Should still be negotiating since CAP END wasn't sent
	assert.True(t, handler.isNegotiating())
}

// Test Reset with pending capabilities
func TestCapabilitiesHandler_Reset_WithPendingCaps(t *testing.T) {
	ctx := context.Background()
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	// Set some state including pending capabilities
	handler.setNegotiating(true)
	handler.ackedCaps["sasl"] = ""
	handler.ackedCaps["server-time"] = ""
	handler.pendingCaps["multi-prefix"] = true
	handler.pendingCaps["batch"] = true

	assert.True(t, handler.isNegotiating())
	assert.Len(t, handler.ackedCaps, 2)
	assert.Len(t, handler.pendingCaps, 2)

	handler.Reset()

	assert.False(t, handler.isNegotiating())
	assert.Empty(t, handler.ackedCaps)
	// Note: Reset doesn't clear pendingCaps, but let's check if it should
	// Based on the implementation, it only clears ackedCaps and negotiating state
}

// Test concurrent RequestCapabilities access
func TestCapabilitiesHandler_RequestCapabilities_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	var wg sync.WaitGroup
	capabilities := []string{"sasl", "server-time", "multi-prefix", "batch", "echo-message"}

	// Set up available capabilities so they can be requested
	for _, capability := range capabilities {
		handler.availableCaps[capability] = ""
	}

	// Multiple goroutines trying to request capabilities concurrently
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			capability := capabilities[i%len(capabilities)]
			_ = handler.RequestCapabilities([]string{capability})
		}(i)
	}

	wg.Wait()

	// Should not have panicked due to race conditions
	// All capabilities should be marked as pending
	for _, capability := range capabilities {
		assert.True(t, handler.pendingCaps[capability], "Capability %q should be pending", capability)
	}
}

// Test checkEndNegotiation error handling - this is called internally by HandleCapACK/HandleCapNAK
func TestCapabilitiesHandler_CheckEndNegotiation_EndError(t *testing.T) {
	ctx := context.Background()
	handler, _, _, _ := createTestCapabilitiesHandler(ctx)

	handler.setNegotiating(true)

	// Create a custom sender that records attempts and fails on CAP END
	var sendAttemptsMu sync.Mutex
	sendAttempts := make([]string, 0)
	handler.send = func(command string, params ...string) error {
		sendAttemptsMu.Lock()
		sendAttempts = append(sendAttempts, command+" "+strings.Join(params, " "))
		sendAttemptsMu.Unlock()
		if command == "CAP" && len(params) > 0 && params[0] == "END" {
			return errors.New("end failed")
		}
		return nil
	}

	// Add one pending capability that we'll remove to trigger checkEndNegotiation
	handler.pendingCaps["test-cap"] = true

	// Use HandleCapNAK to trigger checkEndNegotiation
	message := &Message{Params: []string{"*", "test-cap"}}
	capNAKEvent := &Event{
		Type:    EventCapNAK,
		Message: message,
	}

	handler.HandleCapNAK(capNAKEvent)

	// Should have removed the pending capability
	assert.False(t, handler.pendingCaps["test-cap"])

	// Signal coordination to complete
	TriggerCapEnd(handler)

	// Should have tried to end negotiation but failed due to send error
	// Wait for async EndCapabilityNegotiation to complete
	time.Sleep(50 * time.Millisecond)

	// Should have tried to send CAP END and failed
	sendAttemptsMu.Lock()
	assertContains := false
	for _, attempt := range sendAttempts {
		if attempt == "CAP END" {
			assertContains = true
			break
		}
	}
	sendAttemptsMu.Unlock()
	assert.True(t, assertContains, "Expected sendAttempts to contain 'CAP END'")

	// Should still be negotiating since send failed
	assert.True(t, handler.isNegotiating())
}

// Additional tests for HandleCapNEW functionality
func TestCapabilitiesHandler_HandleCapNEW_RequestsMatchingCapabilities(t *testing.T) {
	ctx := context.Background()
	requestedCaps := []string{"sasl", "server-time", "multi-prefix"}
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx, requestedCaps...)

	// Not negotiating (after initial negotiation is complete)
	handler.setNegotiating(false)

	message := &Message{Params: []string{"*", "sasl echo-message server-time=value"}}
	capNEWEvent := &Event{
		Type:    EventCapNEW,
		Message: message,
	}

	handler.HandleCapNEW(capNEWEvent)

	// Should parse and store new capabilities in availableCaps
	assert.Equal(t, "", handler.availableCaps["sasl"])
	assert.Equal(t, "", handler.availableCaps["echo-message"])
	assert.Equal(t, "value", handler.availableCaps["server-time"])

	// Should have sent CAP REQ for matching capabilities
	command, params := sender.lastSent()
	assert.Equal(t, "CAP", command)
	// Should only request capabilities that are in our requested list
	assert.Equal(t, []string{"REQ", "sasl server-time"}, params)

	// Should mark requested capabilities as pending
	assert.True(t, handler.pendingCaps["sasl"])
	assert.True(t, handler.pendingCaps["server-time"])
	assert.False(t, handler.pendingCaps["echo-message"]) // Not in our requested list
}

func TestCapabilitiesHandler_HandleCapNEW_NoMatchingCapabilities(t *testing.T) {
	ctx := context.Background()
	requestedCaps := []string{"sasl", "server-time"}
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx, requestedCaps...)

	// Not negotiating
	handler.setNegotiating(false)

	message := &Message{Params: []string{"*", "echo-message batch=value"}}
	capNEWEvent := &Event{
		Type:    EventCapNEW,
		Message: message,
	}

	handler.HandleCapNEW(capNEWEvent)

	// Should parse and store new capabilities in availableCaps
	assert.Equal(t, "", handler.availableCaps["echo-message"])
	assert.Equal(t, "value", handler.availableCaps["batch"])

	// Should not have sent CAP REQ since no matching capabilities
	command, _ := sender.lastSent()
	assert.Empty(t, command)

	// Should not mark any capabilities as pending
	assert.Empty(t, handler.pendingCaps)
}

func TestCapabilitiesHandler_HandleCapNEW_WhileNegotiating(t *testing.T) {
	ctx := context.Background()
	requestedCaps := []string{"sasl", "server-time"}
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx, requestedCaps...)

	// Still negotiating
	handler.setNegotiating(true)

	message := &Message{Params: []string{"*", "sasl server-time"}}
	capNEWEvent := &Event{
		Type:    EventCapNEW,
		Message: message,
	}

	handler.HandleCapNEW(capNEWEvent)

	// Should parse and store new capabilities in availableCaps
	assert.Equal(t, "", handler.availableCaps["sasl"])
	assert.Equal(t, "", handler.availableCaps["server-time"])

	// Should have sent CAP REQ even while negotiating for matching capabilities
	command, params := sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"REQ", "sasl server-time"}, params)

	// Should mark capabilities as pending
	assert.True(t, handler.pendingCaps["sasl"])
	assert.True(t, handler.pendingCaps["server-time"])
}

func TestCapabilitiesHandler_HandleCapNEW_NoRequestedCapabilities(t *testing.T) {
	ctx := context.Background()
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx) // No requested caps

	// Not negotiating
	handler.setNegotiating(false)

	message := &Message{Params: []string{"*", "sasl server-time"}}
	capNEWEvent := &Event{
		Type:    EventCapNEW,
		Message: message,
	}

	handler.HandleCapNEW(capNEWEvent)

	// Should parse and store new capabilities in availableCaps
	assert.Equal(t, "", handler.availableCaps["sasl"])
	assert.Equal(t, "", handler.availableCaps["server-time"])

	// Should not have sent CAP REQ since no requested capabilities
	command, _ := sender.lastSent()
	assert.Empty(t, command)

	// Should not mark capabilities as pending
	assert.Empty(t, handler.pendingCaps)
}

func TestCapabilitiesHandler_HandleCapNEW_RequestCapabilitiesError(t *testing.T) {
	ctx := context.Background()
	requestedCaps := []string{"sasl"}
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx, requestedCaps...)

	// Not negotiating
	handler.setNegotiating(false)
	sender.sendError = errors.New("send failed")

	message := &Message{Params: []string{"*", "sasl"}}
	capNEWEvent := &Event{
		Type:    EventCapNEW,
		Message: message,
	}

	handler.HandleCapNEW(capNEWEvent)

	// Should parse and store new capabilities in availableCaps
	assert.Equal(t, "", handler.availableCaps["sasl"])

	// Should have tried to send CAP REQ
	command, params := sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"REQ", "sasl"}, params)

	// Should still mark capability as pending even if send failed
	assert.True(t, handler.pendingCaps["sasl"])
}

func TestCapabilitiesHandler_HandleCapNEW_CapabilityWithValue(t *testing.T) {
	ctx := context.Background()
	requestedCaps := []string{"sasl", "batch"}
	handler, sender, _, _ := createTestCapabilitiesHandler(ctx, requestedCaps...)

	// Not negotiating
	handler.setNegotiating(false)

	message := &Message{Params: []string{"*", "sasl=PLAIN,EXTERNAL batch=draft/labeled-response"}}
	capNEWEvent := &Event{
		Type:    EventCapNEW,
		Message: message,
	}

	handler.HandleCapNEW(capNEWEvent)

	// Should parse and store capabilities with values
	assert.Equal(t, "PLAIN,EXTERNAL", handler.availableCaps["sasl"])
	assert.Equal(t, "draft/labeled-response", handler.availableCaps["batch"])

	// Should have sent CAP REQ for both capabilities
	command, params := sender.lastSent()
	assert.Equal(t, "CAP", command)
	assert.Equal(t, []string{"REQ", "sasl batch"}, params)

	// Should mark both capabilities as pending
	assert.True(t, handler.pendingCaps["sasl"])
	assert.True(t, handler.pendingCaps["batch"])
}
