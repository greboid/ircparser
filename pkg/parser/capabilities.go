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
	send           func(command string, params ...string) error
	subscribe      func(EventType, EventHandler) int
	emit           func(event *Event)
	countListeners func(EventType) int
	ctx            context.Context
	cancel         context.CancelFunc

	requestedCaps    []string
	pendingCaps      map[string]bool
	pendingMux       sync.RWMutex
	availableCaps    map[string]string
	availableCapsMux sync.RWMutex
	ackedCaps        map[string]string
	ackedCapsMux     sync.RWMutex

	negotiating    bool
	negotiatingMux sync.RWMutex

	readyCh chan struct{}
}

func NewCapabilitiesHandler(
	ctx context.Context,
	send func(command string, params ...string) error,
	subscribe func(EventType, EventHandler) int,
	emit func(event *Event),
	countListeners func(EventType) int,
	capabilities map[string]string, caps ...string,
) *CapabilitiesHandler {
	ctx, cancel := context.WithCancel(ctx)

	h := &CapabilitiesHandler{
		send:           send,
		emit:           emit,
		subscribe:      subscribe,
		countListeners: countListeners,
		ctx:            ctx,
		cancel:         cancel,
		requestedCaps:  caps,
		pendingCaps:    make(map[string]bool),
		availableCaps:  make(map[string]string),
		ackedCaps:      capabilities,
		readyCh:        make(chan struct{}, 100), // Buffered for multiple handlers
	}

	subscribe(EventConnected, h.HandleConnected)
	subscribe(EventCapLS, h.HandleCapLS)
	subscribe(EventCapACK, h.HandleCapACK)
	subscribe(EventCapNAK, h.HandleCapNAK)
	subscribe(EventCapDEL, h.HandleCapDEL)
	subscribe(EventCapNEW, h.HandleCapNEW)
	subscribe(EventCapEnd, h.HandleCapEnd)
	subscribe(EventCapEndReady, h.HandleCapEndReady)

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

	var capsStr string
	if event.Message != nil {
		capsStr = event.Message.GetTrailing()
	}

	err := ch.processNewCapabilities(capsStr)
	if err != nil {
		slog.Error("Failed to request capabilities", "error", err)
		return
	}

	if len(ch.requestedCaps) == 0 || !ch.hasPendingCapabilities() {
		var reason string
		if len(ch.requestedCaps) == 0 {
			reason = "No capabilities requested"
		} else {
			reason = "No requested capabilities are available"
		}
		slog.Debug(reason + ", ending negotiation")
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

		ch.emit(&Event{
			Type: EventCapAdded,
			Data: &CapAddedData{
				Capability: capability,
			},
			Time: time.Now(),
		})
	}

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

	for _, capability := range nakedCaps {
		ch.removePendingCapability(capability)
	}

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

	slog.Info("Server advertised new capabilities", "capabilities_string", capsStr)

	err := ch.processNewCapabilities(capsStr)
	if err != nil {
		slog.Error("Failed to request new capabilities", "error", err)
	}
}

func (ch *CapabilitiesHandler) RequestCapabilities(capabilities []string) error {
	if len(capabilities) == 0 {
		return nil
	}

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

	capValues := ch.getCapabilityValues()

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

	// Wait for handlers to signal completion or timeout
	go func() {
		expectedHandlers := ch.countListeners(EventCapPreEnd)
		readyCount := 0

		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()

		slog.Debug("Waiting for handlers to signal ready", "expected", expectedHandlers)

		if expectedHandlers == 0 {
			// No handlers registered, proceed immediately
			slog.Debug("No handlers registered for EventCapPreEnd, proceeding with CAP END")
			ch.HandleCapEnd(&Event{Type: EventCapEnd, Time: time.Now()})
			return
		}

		for {
			select {
			case <-ch.readyCh:
				readyCount++
				slog.Debug("Handler signaled ready", "ready_count", readyCount, "expected", expectedHandlers)
				if readyCount >= expectedHandlers {
					slog.Debug("All handlers signaled ready, proceeding with CAP END")
					ch.HandleCapEnd(&Event{Type: EventCapEnd, Time: time.Now()})
					return
				}
			case <-timer.C:
				slog.Warn("Timeout waiting for handlers to complete", "ready_count", readyCount, "expected", expectedHandlers)
				ch.HandleCapEnd(&Event{Type: EventCapEnd, Time: time.Now()})
				return
			case <-ch.ctx.Done():
				slog.Debug("Context cancelled while waiting for handlers")
				return
			}
		}
	}()

	return nil
}

func (ch *CapabilitiesHandler) HandleCapEnd(event *Event) {
	slog.Debug("Handlers signaled completion")

	err := ch.send("CAP", "END")
	if err != nil {
		slog.Error("Failed to send CAP END", "error", err)
		return
	}

	ch.setNegotiating(false)
	slog.Debug("Capability negotiation ended")
}

func (ch *CapabilitiesHandler) HandleCapEndReady(event *Event) {
	select {
	case ch.readyCh <- struct{}{}:
		slog.Debug("Handler signaled ready for cap end")
	default:
		slog.Debug("Ready channel full, signal dropped")
	}
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

	if _, exists := ch.ackedCaps[capability]; exists {
		slog.Debug("Capability already exists, not overwriting", "capability", capability)
		return
	}

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

func (ch *CapabilitiesHandler) processNewCapabilities(capsStr string) error {
	ch.parseAvailableCapabilities(capsStr)

	if len(ch.requestedCaps) == 0 {
		return nil
	}

	newCaps := strings.Fields(capsStr)
	capsToRequest := make([]string, 0)

	for _, newCap := range newCaps {
		capName := newCap
		if strings.Contains(newCap, "=") {
			capName = strings.SplitN(newCap, "=", 2)[0]
		}
		for _, requestedCap := range ch.requestedCaps {
			if requestedCap == capName {
				capsToRequest = append(capsToRequest, requestedCap)
				break
			}
		}
	}

	if len(capsToRequest) > 0 {
		slog.Info("Requesting newly available capabilities", "capabilities", capsToRequest)
		return ch.RequestCapabilities(capsToRequest)
	}

	return nil
}
