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
	send               func(command string, params ...string) error
	emit               func(event *Event)
	unsubscribe        func(EventType, int)
	config             *ConnectionConfig
	ctx                context.Context
	cancel             context.CancelFunc
	state              RegistrationState
	stateMux           sync.RWMutex
	saslRequired       bool
	saslMux            sync.RWMutex
	timeout            time.Duration
	timer              *time.Timer
	timerMux           sync.Mutex
	nickInUseHandlerID int
}

func NewRegistrationHandler(ctx context.Context, config *ConnectionConfig, send func(command string, params ...string) error, subscribe func(EventType, EventHandler) int, unsubscribe func(EventType, int), emit func(event *Event)) *RegistrationHandler {
	slog.Debug("Creating new registration handler", "timeout", DefaultRegistrationTimeout)
	ctx, cancel := context.WithCancel(ctx)

	h := &RegistrationHandler{
		send:        send,
		emit:        emit,
		config:      config,
		ctx:         ctx,
		cancel:      cancel,
		state:       RegStateInactive,
		timeout:     DefaultRegistrationTimeout,
		unsubscribe: unsubscribe,
	}
	subscribe(EventConnected, h.HandleConnected)
	subscribe(EventRegistered, h.HandleWelcome)
	h.nickInUseHandlerID = subscribe(EventNumeric, h.HandleNumeric)
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
	slog.Info("Sending IRC registration commands", "nick", rh.config.Nick, "username", rh.config.Username, "has_password", rh.config.Password != "")

	if rh.config.Password != "" {
		rh.send("PASS", rh.config.Password)
	}

	rh.send("NICK", rh.config.Nick)
	rh.send("USER", rh.config.Username, UserModeVisible, UserServerWildcard, rh.config.Realname)
}

func (rh *RegistrationHandler) startTimeout() {
	rh.timerMux.Lock()
	defer rh.timerMux.Unlock()

	if rh.timer != nil {
		rh.timer.Stop()
	}

	rh.timer = time.AfterFunc(rh.timeout, func() {
		rh.setState(RegStateFailed)
		rh.emit(&Event{
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

func (rh *RegistrationHandler) HandleNumeric(event *Event) {
	if !rh.IsActive() {
		return
	}

	numericData, ok := event.Data.(*NumericData)
	if !ok {
		return
	}

	if numericData.Code == "433" {
		slog.Info("Nick in use, trying with underscore", "current_nick", rh.config.Nick)
		rh.config.Nick = rh.config.Nick + "_"
		rh.send("NICK", rh.config.Nick)
	}
}

func (rh *RegistrationHandler) HandleWelcome(event *Event) {
	if !rh.IsActive() {
		return
	}

	rh.setState(RegStateComplete)
	rh.stopTimeout()

	if rh.unsubscribe != nil && rh.nickInUseHandlerID != 0 {
		rh.unsubscribe(EventNumeric, rh.nickInUseHandlerID)
	}

	rh.emit(&Event{
		Type: EventRegistered,
		Data: &ConnectedData{
			ServerName: event.Message.Source,
		},
	})
}

func (rh *RegistrationHandler) GetTimeout() time.Duration {
	return rh.timeout
}

func (rh *RegistrationHandler) SetTimeout(timeout time.Duration) {
	rh.timeout = timeout
}
