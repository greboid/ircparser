package parser

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Registration constants
const (
	DefaultRegistrationTimeout = 60 * time.Second
	CapabilityProtocolVersion  = "302"
	UserModeVisible            = "0"
	UserServerWildcard         = "*"
)

type RegistrationState int

const (
	RegStateInactive RegistrationState = iota
	RegStateStarted
	RegStateCapNegotiation
	RegStateSASL
	RegStateComplete
	RegStateFailed
)

func (rs RegistrationState) String() string {
	switch rs {
	case RegStateInactive:
		return "inactive"
	case RegStateStarted:
		return "started"
	case RegStateCapNegotiation:
		return "cap_negotiation"
	case RegStateSASL:
		return "sasl"
	case RegStateComplete:
		return "complete"
	case RegStateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

type RegistrationHandler struct {
	connection *Connection
	eventBus   *EventBus
	ctx        context.Context
	cancel     context.CancelFunc

	state    RegistrationState
	stateMux sync.RWMutex

	capNegotiation bool
	capNegMux      sync.RWMutex

	saslRequired bool
	saslMux      sync.RWMutex

	timeout  time.Duration
	timer    *time.Timer
	timerMux sync.Mutex

	requestedCaps []string
	requestedMux  sync.RWMutex

	pendingCaps []string
	pendingMux  sync.RWMutex
}

func NewRegistrationHandler(ctx context.Context, connection *Connection, eventBus *EventBus) *RegistrationHandler {
	slog.Debug("Creating new registration handler", "timeout", DefaultRegistrationTimeout)
	ctx, cancel := context.WithCancel(ctx)

	h := &RegistrationHandler{
		connection:    connection,
		eventBus:      eventBus,
		ctx:           ctx,
		cancel:        cancel,
		state:         RegStateInactive,
		timeout:       DefaultRegistrationTimeout,
		requestedCaps: []string{"sasl", "echo-message"},
	}
	eventBus.Subscribe(EventConnected, h.HandleConnected)
	eventBus.Subscribe(EventCapLS, h.HandleCapLS)
	eventBus.Subscribe(EventCapACK, h.HandleCapACK)
	eventBus.Subscribe(EventSASLSuccess, h.HandleSASLSuccess)
	eventBus.Subscribe(EventSASLFail, h.HandleSASLFail)
	eventBus.Subscribe(EventRegistered, h.HandleWelcome)
	return h
}

func (rh *RegistrationHandler) GetState() RegistrationState {
	rh.stateMux.RLock()
	defer rh.stateMux.RUnlock()
	return rh.state
}

func (rh *RegistrationHandler) setState(state RegistrationState) {
	rh.stateMux.Lock()
	defer rh.stateMux.Unlock()
	old := rh.state
	rh.state = state
	slog.Debug("Registration state changed", "old_state", old.String(), "new_state", state.String())
}

func (rh *RegistrationHandler) IsActive() bool {
	state := rh.GetState()
	return state != RegStateInactive && state != RegStateComplete && state != RegStateFailed
}

func (rh *RegistrationHandler) IsComplete() bool {
	return rh.GetState() == RegStateComplete
}

func (rh *RegistrationHandler) IsFailed() bool {
	return rh.GetState() == RegStateFailed
}

func (rh *RegistrationHandler) SetRequestedCapabilities(caps []string) {
	rh.requestedMux.Lock()
	defer rh.requestedMux.Unlock()
	rh.requestedCaps = caps
}

func (rh *RegistrationHandler) GetRequestedCapabilities() []string {
	rh.requestedMux.RLock()
	defer rh.requestedMux.RUnlock()
	caps := make([]string, len(rh.requestedCaps))
	copy(caps, rh.requestedCaps)
	return caps
}

func (rh *RegistrationHandler) SetSASLRequired(required bool) {
	rh.saslMux.Lock()
	defer rh.saslMux.Unlock()
	rh.saslRequired = required
}

func (rh *RegistrationHandler) IsSASLRequired() bool {
	rh.saslMux.RLock()
	defer rh.saslMux.RUnlock()
	return rh.saslRequired
}

func (rh *RegistrationHandler) Start() error {
	if rh.IsActive() {
		slog.Warn("Registration start requested but already in progress", "current_state", rh.GetState().String())
		return fmt.Errorf("registration already in progress")
	}

	slog.Info("Starting IRC registration", "timeout", rh.timeout)
	rh.setState(RegStateStarted)
	rh.startTimeout()

	if rh.hasCapabilityNegotiation() {
		slog.Info("Starting capability negotiation", "requested_capabilities", rh.GetRequestedCapabilities())
		rh.setState(RegStateCapNegotiation)
		rh.setCapNegotiation(true)

		err := rh.connection.Send("CAP", "LS", CapabilityProtocolVersion)
		if err != nil {
			rh.setState(RegStateFailed)
			slog.Error("Failed to request capabilities", "error", err)
			return fmt.Errorf("failed to request capabilities: %w", err)
		}

		// Send NICK/USER immediately after CAP LS to avoid deadlock with non-capability servers
		slog.Info("Sending registration commands after CAP LS to prevent deadlock")
		rh.sendRegistrationCommands()
	} else {
		slog.Info("No capability negotiation requested, sending registration commands directly")
		rh.sendRegistrationCommands()
	}

	return nil
}

func (rh *RegistrationHandler) hasCapabilityNegotiation() bool {
	caps := rh.GetRequestedCapabilities()
	return len(caps) > 0
}

func (rh *RegistrationHandler) setCapNegotiation(active bool) {
	rh.capNegMux.Lock()
	defer rh.capNegMux.Unlock()
	rh.capNegotiation = active
}

func (rh *RegistrationHandler) isCapNegotiationActive() bool {
	rh.capNegMux.RLock()
	defer rh.capNegMux.RUnlock()
	return rh.capNegotiation
}

func (rh *RegistrationHandler) sendRegistrationCommands() {
	config := rh.connection.GetConfig()
	slog.Info("Sending IRC registration commands", "nick", config.Nick, "username", config.Username, "has_password", config.Password != "")

	if config.Password != "" {
		rh.connection.Send("PASS", config.Password)
	}

	rh.connection.Send("NICK", config.Nick)
	rh.connection.Send("USER", config.Username, UserModeVisible, UserServerWildcard, config.Realname)
}

func (rh *RegistrationHandler) startTimeout() {
	rh.timerMux.Lock()
	defer rh.timerMux.Unlock()

	if rh.timer != nil {
		rh.timer.Stop()
	}

	rh.timer = time.AfterFunc(rh.timeout, func() {
		rh.setState(RegStateFailed)
		rh.eventBus.Emit(&Event{
			Type: EventError,
			Data: &ErrorData{
				Message: "registration timeout",
			},
		})
	})
}

func (rh *RegistrationHandler) stopTimeout() {
	rh.timerMux.Lock()
	defer rh.timerMux.Unlock()

	if rh.timer != nil {
		rh.timer.Stop()
		rh.timer = nil
	}
}

func (rh *RegistrationHandler) HandleConnected(event *Event) {
	if rh.GetState() == RegStateInactive {
		rh.Start()
	}
}

func (rh *RegistrationHandler) HandleCapLS(event *Event) {
	if !rh.isCapNegotiationActive() {
		slog.Debug("Received CAP LS but capability negotiation not active")
		return
	}

	if event.Message == nil || len(event.Message.Params) < 2 {
		slog.Warn("Invalid CAP LS message format", "message", event.Message)
		return
	}

	capsStr := event.Message.GetTrailing()
	serverCaps := strings.Fields(capsStr)
	slog.Info("Server capabilities received", "capabilities", serverCaps)

	var requestCaps []string
	requestedCaps := rh.GetRequestedCapabilities()

	for _, requestedCap := range requestedCaps {
		for _, serverCap := range serverCaps {
			capName := serverCap
			if strings.Contains(serverCap, "=") {
				capName = strings.SplitN(serverCap, "=", 2)[0]
			}

			if capName == requestedCap {
				requestCaps = append(requestCaps, requestedCap)
				slog.Debug("Server supports requested capability", "capability", requestedCap)
				break
			}
		}
	}

	rh.setPendingCaps(requestCaps)

	if len(requestCaps) > 0 {
		slog.Info("Requesting capabilities from server", "capabilities", requestCaps)
		rh.connection.Send("CAP", "REQ", strings.Join(requestCaps, " "))
	} else {
		slog.Info("No supported capabilities to request, ending capability negotiation")
		rh.endCapNegotiation()
	}
}

func (rh *RegistrationHandler) HandleCapACK(event *Event) {
	if !rh.isCapNegotiationActive() {
		slog.Debug("Received CAP ACK but capability negotiation not active")
		return
	}

	if event.Message == nil {
		slog.Warn("Invalid CAP ACK message - no message")
		return
	}

	capsStr := event.Message.GetTrailing()
	ackedCaps := strings.Fields(capsStr)
	slog.Info("Server acknowledged capabilities", "capabilities", ackedCaps)

	rh.removePendingCaps(ackedCaps)

	config := rh.connection.GetConfig()
	hasSASL := false
	for _, cap := range ackedCaps {
		if cap == "sasl" {
			hasSASL = true
			break
		}
	}

	if hasSASL && config.SASLUser != "" && config.SASLPass != "" {
		slog.Info("SASL capability acknowledged, starting SASL authentication")
		rh.setState(RegStateSASL)
	} else {
		slog.Debug("No SASL authentication needed, checking remaining capabilities")
		rh.checkPendingCaps()
	}
}

func (rh *RegistrationHandler) HandleCapNAK(event *Event) {
	if !rh.isCapNegotiationActive() {
		return
	}

	if event.Message == nil {
		return
	}

	capsStr := event.Message.GetTrailing()
	nakedCaps := strings.Fields(capsStr)

	rh.removePendingCaps(nakedCaps)

	for _, cap := range nakedCaps {
		if cap == "sasl" && rh.IsSASLRequired() {
			rh.setState(RegStateFailed)
			rh.eventBus.Emit(&Event{
				Type: EventError,
				Data: &ErrorData{
					Message: "SASL required but not supported by server",
				},
			})
			return
		}
	}

	rh.checkPendingCaps()
}

func (rh *RegistrationHandler) HandleSASLSuccess(event *Event) {
	if rh.GetState() == RegStateSASL {
		rh.checkPendingCaps()
	}
}

func (rh *RegistrationHandler) HandleSASLFail(event *Event) {
	if rh.GetState() == RegStateSASL {
		if rh.IsSASLRequired() {
			rh.setState(RegStateFailed)
			rh.eventBus.Emit(&Event{
				Type: EventError,
				Data: &ErrorData{
					Message: "SASL authentication failed and is required",
				},
			})
		} else {
			rh.checkPendingCaps()
		}
	}
}

func (rh *RegistrationHandler) setPendingCaps(caps []string) {
	rh.pendingMux.Lock()
	defer rh.pendingMux.Unlock()
	rh.pendingCaps = caps
}

func (rh *RegistrationHandler) removePendingCaps(caps []string) {
	rh.pendingMux.Lock()
	defer rh.pendingMux.Unlock()

	for _, cap := range caps {
		for i, pending := range rh.pendingCaps {
			if pending == cap {
				rh.pendingCaps = append(rh.pendingCaps[:i], rh.pendingCaps[i+1:]...)
				break
			}
		}
	}
}

func (rh *RegistrationHandler) hasPendingCaps() bool {
	rh.pendingMux.RLock()
	defer rh.pendingMux.RUnlock()
	return len(rh.pendingCaps) > 0
}

func (rh *RegistrationHandler) checkPendingCaps() {
	if !rh.hasPendingCaps() {
		rh.endCapNegotiation()
	}
}

func (rh *RegistrationHandler) endCapNegotiation() {
	slog.Info("Ending capability negotiation")
	rh.setCapNegotiation(false)
	rh.connection.Send("CAP", "END")
	// Note: NICK/USER commands were already sent after CAP LS to avoid deadlock
}

func (rh *RegistrationHandler) HandleWelcome(event *Event) {
	if !rh.IsActive() {
		return
	}

	slog.Info("Received 001 welcome message", "capability_negotiation_active", rh.isCapNegotiationActive())

	// If capability negotiation is active, it means the server doesn't support capabilities
	// since it sent 001 before responding to CAP LS
	if rh.isCapNegotiationActive() {
		slog.Info("Server sent 001 during capability negotiation - server doesn't support capabilities")
		rh.setCapNegotiation(false)
		rh.setPendingCaps(nil)
	}

	rh.setState(RegStateComplete)
	rh.stopTimeout()

	rh.eventBus.Emit(&Event{
		Type: EventRegistered,
		Data: &ConnectedData{
			ServerName: rh.connection.GetServerName(),
		},
	})
}

func (rh *RegistrationHandler) HandleRegistered(event *Event) {
	// This method is kept for backwards compatibility but the real logic
	// is now in HandleWelcome which is called when 001 is received
	rh.HandleWelcome(event)
}

func (rh *RegistrationHandler) HandleNickInUse(event *Event) {
	if rh.IsActive() {
		config := rh.connection.GetConfig()
		newNick := config.Nick + "_"
		rh.connection.Send("NICK", newNick)
	}
}

func (rh *RegistrationHandler) HandleError(event *Event) {
	if rh.IsActive() {
		rh.setState(RegStateFailed)
		rh.stopTimeout()
	}
}

func (rh *RegistrationHandler) Reset() {
	rh.setState(RegStateInactive)
	rh.setCapNegotiation(false)
	rh.stopTimeout()
	rh.setPendingCaps(nil)
}

func (rh *RegistrationHandler) GetTimeout() time.Duration {
	return rh.timeout
}

func (rh *RegistrationHandler) SetTimeout(timeout time.Duration) {
	rh.timeout = timeout
}

func (rh *RegistrationHandler) ForceComplete() {
	if rh.IsActive() {
		rh.setState(RegStateComplete)
		rh.stopTimeout()
	}
}

func (rh *RegistrationHandler) AddRequestedCapability(cap string) {
	rh.requestedMux.Lock()
	defer rh.requestedMux.Unlock()

	for _, existing := range rh.requestedCaps {
		if existing == cap {
			return
		}
	}

	rh.requestedCaps = append(rh.requestedCaps, cap)
}

func (rh *RegistrationHandler) RemoveRequestedCapability(cap string) {
	rh.requestedMux.Lock()
	defer rh.requestedMux.Unlock()

	for i, existing := range rh.requestedCaps {
		if existing == cap {
			rh.requestedCaps = append(rh.requestedCaps[:i], rh.requestedCaps[i+1:]...)
			break
		}
	}
}
