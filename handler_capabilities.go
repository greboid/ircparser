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
	send        func(command string, params ...string) error
	subscribe   func(EventType, EventHandler) int
	emit        func(event *Event)
	createCoord func(name string) <-chan struct{}
	removeCoord func(name string)
	ctx         context.Context
	cancel      context.CancelFunc

	requestedCaps    []string
	pendingCaps      map[string]bool
	pendingMux       sync.RWMutex
	availableCaps    map[string]string // Capabilities from CAP LS
	availableCapsMux sync.RWMutex
	ackedCaps        map[string]string // Active capabilities from CAP ACK
	ackedCapsMux     sync.RWMutex

	negotiating    bool
	negotiatingMux sync.RWMutex
}

func NewCapabilitiesHandler(
	ctx context.Context,
	send func(command string, params ...string) error,
	subscribe func(EventType, EventHandler) int,
	emit func(event *Event),
	createCoord func(name string) <-chan struct{},
	removeCoord func(name string),
	capabilities map[string]string, caps ...string,
) *CapabilitiesHandler {
	ctx, cancel := context.WithCancel(ctx)

	h := &CapabilitiesHandler{
		send:          send,
		emit:          emit,
		subscribe:     subscribe,
		removeCoord:   removeCoord,
		createCoord:   createCoord,
		ctx:           ctx,
		cancel:        cancel,
		requestedCaps: caps,
		pendingCaps:   make(map[string]bool),
		availableCaps: make(map[string]string),
		ackedCaps:     capabilities,
	}

	subscribe(EventConnected, h.HandleConnected)
	subscribe(EventCapLS, h.HandleCapLS)
	subscribe(EventCapACK, h.HandleCapACK)
	subscribe(EventCapNAK, h.HandleCapNAK)
	subscribe(EventCapDEL, h.HandleCapDEL)
	subscribe(EventCapNEW, h.HandleCapNEW)

	return h
}

func (ch *CapabilitiesHandler) HandleConnected(*Event) {
	slog.Debug("CapabilitiesHandler: Connected event received, starting capability negotiation")

	ch.setNegotiating(true)

	err := ch.send("CAP", "LS", DefaultCapabilityProtocolVersion)
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

	// Parse and store available capability values from CAP LS response
	if event.Message != nil {
		ch.parseAvailableCapabilities(event.Message.GetTrailing())
	}

	if len(ch.requestedCaps) > 0 {
		err := ch.RequestCapabilities(ch.requestedCaps)
		if err != nil {
			slog.Error("Failed to request capabilities", "error", err)
			return
		}

		// Check if any capabilities are actually pending after filtering
		if !ch.hasPendingCapabilities() {
			slog.Debug("No requested capabilities are available, ending negotiation")
			if err := ch.EndCapabilityNegotiation(); err != nil {
				slog.Error("Failed to end capability negotiation", "error", err)
			}
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
		ch.emit(&Event{
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
		ch.emit(&Event{
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

	// Filter capabilities to only those advertised by the server
	ch.availableCapsMux.RLock()
	availableCaps := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		if _, exists := ch.availableCaps[capability]; exists {
			availableCaps = append(availableCaps, capability)
		} else {
			slog.Debug("Skipping capability not advertised by server", "capability", capability)
		}
	}
	ch.availableCapsMux.RUnlock()

	if len(availableCaps) == 0 {
		slog.Debug("No requested capabilities are available on server")
		return nil
	}

	// Mark available capabilities as pending
	ch.pendingMux.Lock()
	for _, capability := range availableCaps {
		ch.pendingCaps[capability] = true
	}
	ch.pendingMux.Unlock()

	capString := strings.Join(availableCaps, " ")
	err := ch.send("CAP", "REQ", capString)
	if err != nil {
		slog.Error("Failed to request capabilities", "capabilities", availableCaps, "error", err)
		return err
	}

	slog.Debug("Requested capabilities", "capabilities", availableCaps)
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
	coordCh := ch.createCoord("cap-pre-end")

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
	ch.emit(preEndEvent)

	// Wait for handlers asynchronously to avoid blocking the event bus
	go func() {
		defer ch.removeCoord("cap-pre-end")

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
		err := ch.send("CAP", "END")
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
	ch.ackedCapsMux.RLock()
	defer ch.ackedCapsMux.RUnlock()
	_, exists := ch.ackedCaps[capability]
	return exists
}

func (ch *CapabilitiesHandler) GetActiveCaps() []string {
	ch.ackedCapsMux.RLock()
	defer ch.ackedCapsMux.RUnlock()

	caps := make([]string, 0, len(ch.ackedCaps))
	for capability := range ch.ackedCaps {
		caps = append(caps, capability)
	}
	return caps
}

func (ch *CapabilitiesHandler) addCapability(capability string) {
	ch.ackedCapsMux.Lock()
	ch.availableCapsMux.RLock()
	defer ch.ackedCapsMux.Unlock()
	defer ch.availableCapsMux.RUnlock()

	// Don't overwrite existing capability
	if _, exists := ch.ackedCaps[capability]; exists {
		slog.Debug("Capability already exists, not overwriting", "capability", capability)
		return
	}

	// Use the value from available capabilities if it exists, otherwise empty string
	value := ""
	if availableValue, exists := ch.availableCaps[capability]; exists {
		value = availableValue
	}

	ch.ackedCaps[capability] = value
	slog.Debug("Added active capability", "capability", capability, "value", value)
}

func (ch *CapabilitiesHandler) removeCapability(capability string) {
	ch.ackedCapsMux.Lock()
	defer ch.ackedCapsMux.Unlock()
	delete(ch.ackedCaps, capability)
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
	ch.ackedCapsMux.Lock()
	ch.ackedCaps = make(map[string]string)
	ch.ackedCapsMux.Unlock()

	ch.availableCapsMux.Lock()
	ch.availableCaps = make(map[string]string)
	ch.availableCapsMux.Unlock()

	ch.setNegotiating(false)
	slog.Debug("CapabilitiesHandler reset")
}

func (ch *CapabilitiesHandler) parseCapabilities(capsStr string, targetMap map[string]string, logPrefix string) {
	caps := strings.Fields(capsStr)
	for _, capability := range caps {
		if strings.Contains(capability, "=") {
			parts := strings.SplitN(capability, "=", 2)
			if len(parts) == 2 {
				targetMap[parts[0]] = parts[1]
				slog.Debug("Parsed "+logPrefix+" capability with value", "capability", parts[0], "value", parts[1])
			}
		} else {
			targetMap[capability] = ""
			slog.Debug("Parsed "+logPrefix+" capability without value", "capability", capability)
		}
	}
}

func (ch *CapabilitiesHandler) parseAvailableCapabilities(capsStr string) {
	ch.availableCapsMux.Lock()
	defer ch.availableCapsMux.Unlock()
	ch.parseCapabilities(capsStr, ch.availableCaps, "available")
}

func (ch *CapabilitiesHandler) getCapabilityValues() map[string]string {
	ch.ackedCapsMux.RLock()
	defer ch.ackedCapsMux.RUnlock()

	values := make(map[string]string, len(ch.ackedCaps))
	for capability, value := range ch.ackedCaps {
		values[capability] = value
	}
	return values
}
