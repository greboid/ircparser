package parser

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

// SASL constants
const (
	DefaultSASLTimeout   = 30 * time.Second
	DefaultSASLChunkSize = 400
)

type SASLState int

const (
	SASLStateInactive SASLState = iota
	SASLStateRequesting
	SASLStateAuthenticating
	SASLStateSuccess
	SASLStateFailed
)

func (s SASLState) String() string {
	switch s {
	case SASLStateInactive:
		return "inactive"
	case SASLStateRequesting:
		return "requesting"
	case SASLStateAuthenticating:
		return "authenticating"
	case SASLStateSuccess:
		return "success"
	case SASLStateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

type SASLHandler struct {
	subscribe func(EventType, EventHandler) int
	send      func(command string, params ...string) error
	emit      func(event *Event)
	ctx       context.Context
	cancel    context.CancelFunc

	state    SASLState
	stateMux sync.RWMutex

	mechanism SASLMechanism
	mechMux   sync.RWMutex

	timeout  time.Duration
	timer    *time.Timer
	timerMux sync.Mutex

	availableMechs []string
	mechsMux       sync.RWMutex

	maxChunkSize int

	username string
	password string
}

func NewSASLHandler(ctx context.Context,
	send func(command string, params ...string) error,
	subscribe func(EventType, EventHandler) int,
	emit func(event *Event),
	username,
	password string,
) *SASLHandler {
	ctx, cancel := context.WithCancel(ctx)

	h := &SASLHandler{
		send:         send,
		subscribe:    subscribe,
		emit:         emit,
		ctx:          ctx,
		cancel:       cancel,
		state:        SASLStateInactive,
		timeout:      DefaultSASLTimeout,
		maxChunkSize: DefaultSASLChunkSize,
		username:     username,
		password:     password,
	}

	slog.Debug("Creating SASL handler", "username", username, "has_password", password != "")
	subscribe(EventSASLAuth, h.HandleAuthenticate)
	subscribe(EventSASLSuccess, h.HandleSASLSuccess)
	subscribe(EventSASLFail, h.HandleSASLFail)
	subscribe(EventCapPreEnd, h.handleSasl)
	subscribe(EventCapEndReady, func(event *Event) {}) // Just for counting
	slog.Debug("SASL handler subscribed to events")
	return h
}

func (sh *SASLHandler) SetMechanism(mechanism SASLMechanism) {
	sh.mechMux.Lock()
	defer sh.mechMux.Unlock()
	sh.mechanism = mechanism
}

func (sh *SASLHandler) GetMechanism() SASLMechanism {
	sh.mechMux.RLock()
	defer sh.mechMux.RUnlock()
	return sh.mechanism
}

func (sh *SASLHandler) getMechanismName() string {
	mechanism := sh.GetMechanism()
	if mechanism != nil {
		return mechanism.Name()
	}
	return "unknown"
}

func (sh *SASLHandler) GetState() SASLState {
	sh.stateMux.RLock()
	defer sh.stateMux.RUnlock()
	return sh.state
}

func (sh *SASLHandler) setState(state SASLState) {
	sh.stateMux.Lock()
	defer sh.stateMux.Unlock()
	sh.state = state
}

func (sh *SASLHandler) GetAvailableMechanisms() []string {
	sh.mechsMux.RLock()
	defer sh.mechsMux.RUnlock()
	mechs := make([]string, len(sh.availableMechs))
	copy(mechs, sh.availableMechs)
	return mechs
}

func (sh *SASLHandler) setAvailableMechanisms(mechs []string) {
	sh.mechsMux.Lock()
	defer sh.mechsMux.Unlock()
	sh.availableMechs = mechs
}

func (sh *SASLHandler) IsAvailable() bool {
	return len(sh.GetAvailableMechanisms()) > 0
}

func (sh *SASLHandler) IsActive() bool {
	state := sh.GetState()
	return state == SASLStateRequesting || state == SASLStateAuthenticating
}

func (sh *SASLHandler) IsComplete() bool {
	state := sh.GetState()
	return state == SASLStateSuccess || state == SASLStateFailed
}

func (sh *SASLHandler) SupportsPlain() bool {
	mechs := sh.GetAvailableMechanisms()
	for _, mech := range mechs {
		if mech == "PLAIN" {
			return true
		}
	}
	return false
}

func (sh *SASLHandler) StartAuthentication() error {
	if sh.GetState() != SASLStateInactive {
		slog.Warn("SASL authentication already in progress", "current_state", sh.GetState())
		return fmt.Errorf("SASL authentication already in progress")
	}

	mechanism := sh.GetMechanism()
	if mechanism == nil {
		slog.Error("No SASL mechanism configured")
		return fmt.Errorf("no SASL mechanism configured")
	}

	if !sh.IsAvailable() {
		slog.Error("SASL not available from server")
		return fmt.Errorf("SASL not available")
	}

	mechs := sh.GetAvailableMechanisms()
	supported := false
	for _, mech := range mechs {
		if mech == mechanism.Name() {
			supported = true
			break
		}
	}

	if !supported {
		slog.Error("SASL mechanism not supported by server",
			"mechanism", mechanism.Name(),
			"available_mechanisms", mechs)
		return fmt.Errorf("mechanism %s not supported by server", mechanism.Name())
	}

	slog.Info("Starting SASL authentication",
		"mechanism", mechanism.Name(),
		"timeout", sh.timeout)

	sh.setState(SASLStateRequesting)

	err := sh.send("AUTHENTICATE", mechanism.Name())
	if err != nil {
		sh.setState(SASLStateFailed)
		slog.Error("Failed to send AUTHENTICATE command", "mechanism", mechanism.Name(), "error", err)
		return fmt.Errorf("failed to request SASL authentication: %w", err)
	}

	sh.startTimeout()

	return nil
}

func (sh *SASLHandler) Abort() error {
	if !sh.IsActive() {
		return fmt.Errorf("SASL authentication not active")
	}

	sh.setState(SASLStateFailed)
	sh.stopTimeout()

	return sh.send("AUTHENTICATE", "*")
}

func (sh *SASLHandler) startTimeout() {
	sh.timerMux.Lock()
	defer sh.timerMux.Unlock()

	if sh.timer != nil {
		sh.timer.Stop()
	}

	sh.timer = time.AfterFunc(sh.timeout, func() {
		sh.setState(SASLStateFailed)

		mechanismName := "unknown"
		mechanism := sh.GetMechanism()
		if mechanism != nil {
			mechanismName = mechanism.Name()
		}

		sh.emit(&Event{
			Type: EventSASLFail,
			Data: &SASLData{
				Mechanism: mechanismName,
				Data:      "timeout",
			},
		})
	})
}

func (sh *SASLHandler) stopTimeout() {
	sh.timerMux.Lock()
	defer sh.timerMux.Unlock()

	if sh.timer != nil {
		sh.timer.Stop()
		sh.timer = nil
	}
}

func (sh *SASLHandler) handleSasl(event *Event) {
	slog.Debug("Starting SASL")
	capPreEndData, ok := event.Data.(*CapPreEndData)
	if !ok {
		slog.Error("Incorrect event type", "type", event.Data)
		sh.emit(&Event{
			Type: EventCapEndReady,
			Time: time.Now().UTC(),
		})
		return
	}
	if !slices.Contains(capPreEndData.ActiveCaps, "sasl") {
		slog.Error("SASL required but not supported by server")
		sh.emit(&Event{
			Type: EventError,
			Data: &ErrorData{
				Message: "SASL required but not supported by server",
			},
		})
		sh.emit(&Event{
			Type: EventCapEndReady,
			Time: time.Now().UTC(),
		})
		return
	}

	// Parse SASL mechanisms from capability value
	if saslValue, exists := capPreEndData.CapValues["sasl"]; exists && saslValue != "" {
		mechanisms := strings.Split(saslValue, ",")
		sh.setAvailableMechanisms(mechanisms)
		slog.Info("SASL mechanisms available", "mechanisms", mechanisms)
	} else {
		// Default to PLAIN if no mechanisms specified
		sh.setAvailableMechanisms([]string{"PLAIN"})
		slog.Info("SASL capability present but no mechanisms specified, defaulting to PLAIN")
	}

	if sh.username == "" && sh.password == "" {
		slog.Error("No username and password specified")
		sh.emit(&Event{
			Type: EventCapEndReady,
			Time: time.Now().UTC(),
		})
		return
	}

	// Start authentication (non-blocking)
	if err := sh.startPlainAuthentication(); err != nil {
		sh.emit(&Event{
			Type: EventSASLFail,
			Data: &SASLData{
				Mechanism: "unknown",
				Data:      fmt.Sprintf("failed to start SASL: %v", err),
			},
		})
		sh.emit(&Event{
			Type: EventCapEndReady,
			Time: time.Now().UTC(),
		})
		return
	}

	slog.Debug("SASL authentication started")
}

func (sh *SASLHandler) HandleAuthenticate(event *Event) {
	slog.Debug("HandleAuthenticate called", "state", sh.GetState(), "has_message", event.Message != nil)
	if event.Message == nil {
		slog.Warn("AUTHENTICATE event has no message")
		return
	}
	if sh.GetState() != SASLStateRequesting {
		slog.Warn("AUTHENTICATE received in wrong state", "current_state", sh.GetState(), "expected_state", "requesting")
		return
	}

	challenge := event.Message.GetTrailing()
	mechanism := sh.GetMechanism()

	if mechanism == nil {
		if err := sh.Abort(); err != nil {
			slog.Error("Failed to abort SASL authentication", "error", err)
		}
		return
	}

	sh.setState(SASLStateAuthenticating)

	var response string
	var err error

	if challenge == "+" {
		response, err = mechanism.Start()
	} else {
		decodedChallenge, decodeErr := base64.StdEncoding.DecodeString(challenge)
		if decodeErr != nil {
			if err := sh.Abort(); err != nil {
				slog.Error("Failed to abort SASL authentication", "error", err)
			}
			return
		}
		response, err = mechanism.Next(string(decodedChallenge))
	}

	if err != nil {
		if abortErr := sh.Abort(); abortErr != nil {
			slog.Error("Failed to abort SASL authentication", "error", abortErr)
		}
		return
	}

	if response == "" {
		if err := sh.send("AUTHENTICATE", "+"); err != nil {
			sh.setState(SASLStateFailed)
			sh.emit(&Event{
				Type: EventSASLFail,
				Data: &SASLData{
					Mechanism: mechanism.Name(),
					Data:      fmt.Sprintf("send failed: %v", err),
				},
			})
		}
		return
	}

	sh.sendAuthData(response)
}

func (sh *SASLHandler) sendAuthData(data string) {
	if len(data) == 0 {
		if err := sh.send("AUTHENTICATE", "+"); err != nil {
			sh.setState(SASLStateFailed)
			sh.emit(&Event{
				Type: EventSASLFail,
				Data: &SASLData{
					Mechanism: sh.getMechanismName(),
					Data:      fmt.Sprintf("send failed: %v", err),
				},
			})
		}
		return
	}

	if len(data) <= sh.maxChunkSize {
		if err := sh.send("AUTHENTICATE", data); err != nil {
			sh.setState(SASLStateFailed)
			sh.emit(&Event{
				Type: EventSASLFail,
				Data: &SASLData{
					Mechanism: sh.getMechanismName(),
					Data:      fmt.Sprintf("send failed: %v", err),
				},
			})
		}
		return
	}

	// Send data in chunks
	for len(data) > 0 {
		chunkSize := sh.maxChunkSize
		if len(data) < chunkSize {
			chunkSize = len(data)
		}

		chunk := data[:chunkSize]
		data = data[chunkSize:]

		if err := sh.send("AUTHENTICATE", chunk); err != nil {
			sh.setState(SASLStateFailed)
			sh.emit(&Event{
				Type: EventSASLFail,
				Data: &SASLData{
					Mechanism: sh.getMechanismName(),
					Data:      fmt.Sprintf("send failed: %v", err),
				},
			})
			return
		}
	}

	// Send final empty chunk to indicate end of data
	if err := sh.send("AUTHENTICATE", "+"); err != nil {
		sh.setState(SASLStateFailed)
		sh.emit(&Event{
			Type: EventSASLFail,
			Data: &SASLData{
				Mechanism: sh.getMechanismName(),
				Data:      fmt.Sprintf("send failed: %v", err),
			},
		})
	}
}

func (sh *SASLHandler) HandleSASLSuccess(event *Event) {
	if !sh.IsActive() {
		return
	}

	sh.setState(SASLStateSuccess)
	sh.stopTimeout()

	mechanism := sh.GetMechanism()
	if mechanism != nil {
		slog.Info("SASL authentication successful", "mechanism", mechanism.Name())
		sh.emit(&Event{
			Type: EventSASLSuccess,
			Data: &SASLData{
				Mechanism: mechanism.Name(),
				Data:      "authentication successful",
			},
		})
	}

	// Signal that SASL authentication is complete
	sh.emit(&Event{
		Type: EventCapEndReady,
		Time: time.Now().UTC(),
	})
}

func (sh *SASLHandler) HandleSASLFail(event *Event) {
	if !sh.IsActive() {
		return
	}

	sh.setState(SASLStateFailed)
	sh.stopTimeout()

	mechanism := sh.GetMechanism()
	mechanismName := "unknown"
	if mechanism != nil {
		mechanismName = mechanism.Name()
		mechanism.Reset()
	}

	reason := "authentication failed"
	if event.Message != nil && len(event.Message.Params) > 1 {
		reason = event.Message.GetTrailing()
	}

	sh.emit(&Event{
		Type: EventSASLFail,
		Data: &SASLData{
			Mechanism: mechanismName,
			Data:      reason,
		},
	})

	// Signal that SASL authentication is complete (even though it failed)
	sh.emit(&Event{
		Type: EventCapEndReady,
		Time: time.Now().UTC(),
	})
}

func (sh *SASLHandler) Reset() {
	sh.setState(SASLStateInactive)
	sh.stopTimeout()

	mechanism := sh.GetMechanism()
	if mechanism != nil {
		mechanism.Reset()
	}
}

func (sh *SASLHandler) GetTimeout() time.Duration {
	return sh.timeout
}

func (sh *SASLHandler) SetTimeout(timeout time.Duration) {
	sh.timeout = timeout
}

func (sh *SASLHandler) GetMaxChunkSize() int {
	return sh.maxChunkSize
}

func (sh *SASLHandler) SetMaxChunkSize(size int) {
	if size > 0 {
		sh.maxChunkSize = size
	}
}

// startPlainAuthentication starts PLAIN SASL authentication if supported
func (sh *SASLHandler) startPlainAuthentication() error {
	if !sh.SupportsPlain() {
		return fmt.Errorf("PLAIN mechanism not supported by server")
	}

	if sh.username == "" || sh.password == "" {
		return fmt.Errorf("username and password required for PLAIN authentication")
	}

	mechanism := NewPlainMechanism(sh.username, sh.password)
	sh.SetMechanism(mechanism)
	return sh.StartAuthentication()
}
