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
		if err := rh.send("PASS", rh.config.Password); err != nil {
			slog.Error("Failed to send PASS command", "error", err)
			rh.setState(RegStateFailed)
			rh.emit(&Event{
				Type: EventError,
				Data: &ErrorData{
					Message: fmt.Sprintf("failed to send PASS command: %v", err),
				},
			})
			return
		}
	}

	if err := rh.send("NICK", rh.config.Nick); err != nil {
		slog.Error("Failed to send NICK command", "error", err)
		rh.setState(RegStateFailed)
		rh.emit(&Event{
			Type: EventError,
			Data: &ErrorData{
				Message: fmt.Sprintf("failed to send NICK command: %v", err),
			},
		})
		return
	}

	if err := rh.send("USER", rh.config.Username, UserModeVisible, UserServerWildcard, rh.config.Realname); err != nil {
		slog.Error("Failed to send USER command", "error", err)
		rh.setState(RegStateFailed)
		rh.emit(&Event{
			Type: EventError,
			Data: &ErrorData{
				Message: fmt.Sprintf("failed to send USER command: %v", err),
			},
		})
		return
	}
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
		if err := rh.Start(); err != nil {
			slog.Error("Failed to start registration", "error", err)
			rh.emit(&Event{
				Type: EventError,
				Data: &ErrorData{
					Message: fmt.Sprintf("failed to start registration: %v", err),
				},
			})
		}
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
		if err := rh.send("NICK", rh.config.Nick); err != nil {
			slog.Error("Failed to send NICK command for nick collision", "error", err, "new_nick", rh.config.Nick)
			rh.setState(RegStateFailed)
			rh.emit(&Event{
				Type: EventError,
				Data: &ErrorData{
					Message: fmt.Sprintf("failed to send NICK command for collision: %v", err),
				},
			})
		}
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
