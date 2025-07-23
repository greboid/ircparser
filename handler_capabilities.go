package parser

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	DefaultCapabilityProtocolVersion = "302"
)

type CapabilitiesHandler struct {
	connection *Connection
	eventBus   *EventBus
	ctx        context.Context
	cancel     context.CancelFunc

	requestedCaps []string
	pendingCaps   map[string]bool
	pendingMux    sync.RWMutex
	capValues     map[string]string
	capValuesMux  sync.RWMutex

	negotiating    bool
	negotiatingMux sync.RWMutex
}

func NewCapabilitiesHandler(ctx context.Context, connection *Connection, eventBus *EventBus, capabilities map[string]string, caps ...string) *CapabilitiesHandler {
	ctx, cancel := context.WithCancel(ctx)

	h := &CapabilitiesHandler{
		connection:    connection,
		eventBus:      eventBus,
		ctx:           ctx,
		cancel:        cancel,
		requestedCaps: caps,
		pendingCaps:   make(map[string]bool),
		capValues:     capabilities,
	}

	eventBus.Subscribe(EventConnected, h.HandleConnected)
	eventBus.Subscribe(EventCapLS, h.HandleCapLS)
	eventBus.Subscribe(EventCapACK, h.HandleCapACK)
	eventBus.Subscribe(EventCapNAK, h.HandleCapNAK)
	eventBus.Subscribe(EventCapDEL, h.HandleCapDEL)
	eventBus.Subscribe(EventCapNEW, h.HandleCapNEW)

	return h
}

func (ch *CapabilitiesHandler) HandleConnected(*Event) {
	slog.Debug("CapabilitiesHandler: Connected event received, starting capability negotiation")

	ch.setNegotiating(true)

	err := ch.connection.Send("CAP", "LS", DefaultCapabilityProtocolVersion)
	if err != nil {
		slog.Error("Failed to send CAP LS", "error", err)
		ch.setNegotiating(false)
		return
	}

	slog.Debug("Sent CAP LS command")
}

func (ch *CapabilitiesHandler) HandleCapLS(event *Event) {
	if !ch.isNegotiating() {
		return
	}

	// Parse and store capability values from CAP LS response
	if event.Message != nil {
		ch.parseCapabilityValues(event.Message.GetTrailing())
	}

	if len(ch.requestedCaps) > 0 {
		err := ch.RequestCapabilities(ch.requestedCaps)
		if err != nil {
			slog.Error("Failed to request capabilities", "error", err)
			return
		}
	} else {
		// No capabilities requested, end negotiation immediately
		slog.Debug("No capabilities requested, ending negotiation")
		if err := ch.EndCapabilityNegotiation(); err != nil {
			slog.Error("Failed to end capability negotiation", "error", err)
		}
	}
}

func (ch *CapabilitiesHandler) HandleCapACK(event *Event) {
	if !ch.isNegotiating() {
		return
	}

	if event.Message == nil {
		slog.Warn("Invalid CAP ACK message - no message")
		return
	}

	capsStr := event.Message.GetTrailing()
	ackedCaps := strings.Fields(capsStr)

	slog.Info("Server acknowledged capabilities", "capabilities", ackedCaps)

	for _, capability := range ackedCaps {
		ch.addCapability(capability)
		ch.removePendingCapability(capability)

		// Emit cap added event
		ch.eventBus.Emit(&Event{
			Type: EventCapAdded,
			Data: &CapAddedData{
				Capability: capability,
			},
			Time: time.Now(),
		})
	}

	// Check if we can end negotiation
	ch.checkEndNegotiation()
}

func (ch *CapabilitiesHandler) HandleCapNAK(event *Event) {
	if !ch.isNegotiating() {
		return
	}

	if event.Message == nil {
		slog.Warn("Invalid CAP NAK message - no message")
		return
	}

	capsStr := event.Message.GetTrailing()
	nakedCaps := strings.Fields(capsStr)

	slog.Warn("Server rejected capabilities", "capabilities", nakedCaps)

	// Remove rejected capabilities from pending
	for _, capability := range nakedCaps {
		ch.removePendingCapability(capability)
	}

	// Check if we can end negotiation
	ch.checkEndNegotiation()
}

func (ch *CapabilitiesHandler) HandleCapDEL(event *Event) {
	if event.Message == nil {
		slog.Warn("Invalid CAP DEL message - no message")
		return
	}

	capsStr := event.Message.GetTrailing()
	removedCaps := strings.Fields(capsStr)

	slog.Info("Server removed capabilities", "capabilities", removedCaps)

	for _, capability := range removedCaps {
		ch.removeCapability(capability)

		// Emit cap removed event
		ch.eventBus.Emit(&Event{
			Type: EventCapRemoved,
			Data: &CapRemovedData{
				Capability: capability,
			},
			Time: time.Now(),
		})
	}
}

func (ch *CapabilitiesHandler) HandleCapNEW(event *Event) {
	if event.Message == nil {
		slog.Warn("Invalid CAP NEW message - no message")
		return
	}

	capsStr := event.Message.GetTrailing()
	newCaps := strings.Fields(capsStr)

	slog.Info("Server advertised new capabilities", "capabilities", newCaps)

	// For now, we just log the new capabilities
	// In a full implementation, you might want to automatically request some capabilities
}

func (ch *CapabilitiesHandler) RequestCapabilities(capabilities []string) error {
	if len(capabilities) == 0 {
		return nil
	}

	// Mark all capabilities as pending
	ch.pendingMux.Lock()
	for _, capability := range capabilities {
		ch.pendingCaps[capability] = true
	}
	ch.pendingMux.Unlock()

	capString := strings.Join(capabilities, " ")
	err := ch.connection.Send("CAP", "REQ", capString)
	if err != nil {
		slog.Error("Failed to request capabilities", "capabilities", capabilities, "error", err)
		return err
	}

	slog.Debug("Requested capabilities", "capabilities", capabilities)
	return nil
}

func (ch *CapabilitiesHandler) EndCapabilityNegotiation() error {
	if !ch.isNegotiating() {
		slog.Debug("Not currently negotiating capabilities, ignoring end request")
		return nil
	}

	activeCaps := ch.GetActiveCaps()
	slog.Info("Ending capability negotiation", "active_capabilities", activeCaps)

	// Get capability values
	capValues := ch.getCapabilityValues()

	// Create coordination channel for handlers that need to complete before CAP END
	coordCh := ch.eventBus.CreateCoordination("cap-pre-end")

	// Emit pre-end event for handlers
	preEndEvent := &Event{
		Type: EventCapPreEnd,
		Data: &CapPreEndData{
			ActiveCaps: activeCaps,
			CapValues:  capValues,
		},
		Time: time.Now(),
	}

	slog.Debug("Emitting pre-cap-end event")
	ch.eventBus.Emit(preEndEvent)

	// Wait for handlers asynchronously to avoid blocking the event bus
	go func() {
		defer ch.eventBus.RemoveCoordination("cap-pre-end")

		// Wait for handlers that need to complete (e.g., SASL authentication)
		// Use a reasonable timeout to avoid hanging indefinitely
		select {
		case <-coordCh:
			slog.Debug("Handlers signaled completion")
		case <-time.After(30 * time.Second):
			slog.Warn("Timeout waiting for handlers to complete, proceeding with CAP END")
		case <-ch.ctx.Done():
			slog.Debug("Context cancelled while waiting for handlers")
			return
		}

		// Now send CAP END
		err := ch.connection.Send("CAP", "END")
		if err != nil {
			slog.Error("Failed to send CAP END", "error", err)
			return
		}

		ch.setNegotiating(false)
		slog.Debug("Capability negotiation ended")
	}()

	return nil
}

func (ch *CapabilitiesHandler) IsCapabilityActive(capability string) bool {
	ch.capValuesMux.RLock()
	defer ch.capValuesMux.RUnlock()
	_, exists := ch.capValues[capability]
	return exists
}

func (ch *CapabilitiesHandler) GetActiveCaps() []string {
	ch.capValuesMux.RLock()
	defer ch.capValuesMux.RUnlock()

	caps := make([]string, 0, len(ch.capValues))
	for capability := range ch.capValues {
		caps = append(caps, capability)
	}
	return caps
}

func (ch *CapabilitiesHandler) addCapability(capability string) {
	ch.capValuesMux.Lock()
	defer ch.capValuesMux.Unlock()
	if _, exists := ch.capValues[capability]; !exists {
		ch.capValues[capability] = ""
	}
	slog.Debug("Added capability", "capability", capability)
}

func (ch *CapabilitiesHandler) removeCapability(capability string) {
	ch.capValuesMux.Lock()
	defer ch.capValuesMux.Unlock()
	delete(ch.capValues, capability)
	slog.Debug("Removed capability", "capability", capability)
}

func (ch *CapabilitiesHandler) removePendingCapability(capability string) {
	ch.pendingMux.Lock()
	defer ch.pendingMux.Unlock()
	delete(ch.pendingCaps, capability)
	slog.Debug("Removed pending capability", "capability", capability)
}

func (ch *CapabilitiesHandler) hasPendingCapabilities() bool {
	ch.pendingMux.RLock()
	defer ch.pendingMux.RUnlock()
	return len(ch.pendingCaps) > 0
}

func (ch *CapabilitiesHandler) checkEndNegotiation() {
	if !ch.hasPendingCapabilities() {
		slog.Debug("No pending capabilities, ending negotiation")
		if err := ch.EndCapabilityNegotiation(); err != nil {
			slog.Error("Failed to end capability negotiation", "error", err)
		}
	}
}

func (ch *CapabilitiesHandler) isNegotiating() bool {
	ch.negotiatingMux.RLock()
	defer ch.negotiatingMux.RUnlock()
	return ch.negotiating
}

func (ch *CapabilitiesHandler) setNegotiating(negotiating bool) {
	ch.negotiatingMux.Lock()
	defer ch.negotiatingMux.Unlock()
	ch.negotiating = negotiating
	slog.Debug("Capability negotiation state changed", "negotiating", negotiating)
}

func (ch *CapabilitiesHandler) Reset() {
	ch.capValuesMux.Lock()
	ch.capValues = make(map[string]string)
	ch.capValuesMux.Unlock()

	ch.setNegotiating(false)
	slog.Debug("CapabilitiesHandler reset")
}

func (ch *CapabilitiesHandler) parseCapabilityValues(capsStr string) {
	ch.capValuesMux.Lock()
	defer ch.capValuesMux.Unlock()

	caps := strings.Fields(capsStr)
	for _, capability := range caps {
		if strings.Contains(capability, "=") {
			parts := strings.SplitN(capability, "=", 2)
			if len(parts) == 2 {
				ch.capValues[parts[0]] = parts[1]
				slog.Debug("Parsed capability with value", "capability", parts[0], "value", parts[1])
			}
		} else {
			ch.capValues[capability] = ""
			slog.Debug("Parsed capability without value", "capability", capability)
		}
	}
}

func (ch *CapabilitiesHandler) getCapabilityValues() map[string]string {
	ch.capValuesMux.RLock()
	defer ch.capValuesMux.RUnlock()

	values := make(map[string]string, len(ch.capValues))
	for capability, value := range ch.capValues {
		values[capability] = value
	}
	return values
}
