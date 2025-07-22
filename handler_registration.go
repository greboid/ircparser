package parser

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Registration constants
const (
	DefaultRegistrationTimeout = 60 * time.Second
	UserModeVisible            = "0"
	UserServerWildcard         = "*"
)

type RegistrationState int

const (
	RegStateInactive RegistrationState = iota
	RegStateStarted
	RegStateComplete
	RegStateFailed
)

func (rs RegistrationState) String() string {
	switch rs {
	case RegStateInactive:
		return "inactive"
	case RegStateStarted:
		return "started"
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

	saslRequired bool
	saslMux      sync.RWMutex

	timeout  time.Duration
	timer    *time.Timer
	timerMux sync.Mutex
}

func NewRegistrationHandler(ctx context.Context, connection *Connection, eventBus *EventBus) *RegistrationHandler {
	slog.Debug("Creating new registration handler", "timeout", DefaultRegistrationTimeout)
	ctx, cancel := context.WithCancel(ctx)

	h := &RegistrationHandler{
		connection: connection,
		eventBus:   eventBus,
		ctx:        ctx,
		cancel:     cancel,
		state:      RegStateInactive,
		timeout:    DefaultRegistrationTimeout,
	}
	eventBus.Subscribe(EventConnected, h.HandleConnected)
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

func (rh *RegistrationHandler) Start() error {
	if rh.IsActive() {
		slog.Warn("Registration start requested but already in progress", "current_state", rh.GetState().String())
		return fmt.Errorf("registration already in progress")
	}

	slog.Info("Starting IRC registration", "timeout", rh.timeout)
	rh.setState(RegStateStarted)
	rh.startTimeout()
	rh.sendRegistrationCommands()

	return nil
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

func (rh *RegistrationHandler) HandleConnected(*Event) {
	if rh.GetState() == RegStateInactive {
		rh.Start()
	}
}

func (rh *RegistrationHandler) HandleWelcome(*Event) {
	if !rh.IsActive() {
		return
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

func (rh *RegistrationHandler) GetTimeout() time.Duration {
	return rh.timeout
}

func (rh *RegistrationHandler) SetTimeout(timeout time.Duration) {
	rh.timeout = timeout
}
