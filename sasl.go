package parser

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
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

type SASLMechanism interface {
	Name() string
	Start() (string, error)
	Next(challenge string) (string, error)
	IsComplete() bool
	Reset()
}

type PlainMechanism struct {
	username string
	password string
	complete bool
}

func (pm *PlainMechanism) Name() string {
	return "PLAIN"
}

func (pm *PlainMechanism) Start() (string, error) {
	if pm.username == "" || pm.password == "" {
		return "", fmt.Errorf("username and password required for PLAIN")
	}

	authString := fmt.Sprintf("%s\x00%s\x00%s", pm.username, pm.username, pm.password)
	encoded := base64.StdEncoding.EncodeToString([]byte(authString))
	pm.complete = true
	return encoded, nil
}

func (pm *PlainMechanism) Next(challenge string) (string, error) {
	return "", fmt.Errorf("PLAIN mechanism does not support challenges")
}

func (pm *PlainMechanism) IsComplete() bool {
	return pm.complete
}

func (pm *PlainMechanism) Reset() {
	pm.complete = false
}

type SASLHandler struct {
	connection *Connection
	eventBus   *EventBus
	ctx        context.Context
	cancel     context.CancelFunc

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
}

func NewSASLHandler(ctx context.Context, connection *Connection, eventBus *EventBus) *SASLHandler {
	ctx, cancel := context.WithCancel(ctx)

	h := &SASLHandler{
		connection:   connection,
		eventBus:     eventBus,
		ctx:          ctx,
		cancel:       cancel,
		state:        SASLStateInactive,
		timeout:      DefaultSASLTimeout,
		maxChunkSize: DefaultSASLChunkSize,
	}

	eventBus.Subscribe(EventCapLS, h.HandleCapLS)
	eventBus.Subscribe(EventCapACK, h.HandleCapACK)
	eventBus.Subscribe(EventCapNAK, h.HandleCapNAK)
	eventBus.Subscribe(EventSASLAuth, h.HandleAuthenticate)
	eventBus.Subscribe(EventSASLSuccess, h.HandleSASLSuccess)
	eventBus.Subscribe(EventSASLFail, h.HandleSASLFail)
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

	err := sh.connection.Send("AUTHENTICATE", mechanism.Name())
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

	return sh.connection.Send("AUTHENTICATE", "*")
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

		sh.eventBus.Emit(&Event{
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

func (sh *SASLHandler) HandleCapLS(event *Event) {
	if event.Message == nil {
		return
	}

	capsStr := event.Message.GetTrailing()
	caps := strings.Fields(capsStr)

	for _, cap := range caps {
		if strings.HasPrefix(cap, "sasl=") {
			mechsStr := strings.TrimPrefix(cap, "sasl=")
			mechs := strings.Split(mechsStr, ",")
			sh.setAvailableMechanisms(mechs)
			slog.Info("SASL mechanisms available from server", "mechanisms", mechs)
			break
		} else if cap == "sasl" {
			sh.setAvailableMechanisms([]string{"PLAIN"})
			slog.Info("SASL capability available from server", "default_mechanism", "PLAIN")
			break
		}
	}
}

func (sh *SASLHandler) HandleCapACK(event *Event) {
	if event.Message == nil {
		return
	}

	capsStr := event.Message.GetTrailing()
	caps := strings.Fields(capsStr)

	for _, cap := range caps {
		if cap == "sasl" {
			config := sh.connection.GetConfig()
			if config.SASLUser != "" && config.SASLPass != "" {
				// Try to start PLAIN SASL authentication
				if err := sh.startPlainAuthentication(config); err != nil {
					sh.eventBus.Emit(&Event{
						Type: EventSASLFail,
						Data: &SASLData{
							Mechanism: "unknown",
							Data:      fmt.Sprintf("failed to start SASL: %v", err),
						},
					})
				}
			}
			break
		}
	}
}

func (sh *SASLHandler) HandleCapNAK(event *Event) {
	if event.Message == nil {
		return
	}

	capsStr := event.Message.GetTrailing()
	caps := strings.Fields(capsStr)

	for _, cap := range caps {
		if cap == "sasl" {
			sh.setState(SASLStateFailed)
			sh.eventBus.Emit(&Event{
				Type: EventSASLFail,
				Data: &SASLData{
					Mechanism: "unknown",
					Data:      "sasl capability not acknowledged",
				},
			})
			break
		}
	}
}

func (sh *SASLHandler) HandleAuthenticate(event *Event) {
	if event.Message == nil || sh.GetState() != SASLStateRequesting {
		return
	}

	challenge := event.Message.GetTrailing()
	mechanism := sh.GetMechanism()

	if mechanism == nil {
		sh.Abort()
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
			sh.Abort()
			return
		}
		response, err = mechanism.Next(string(decodedChallenge))
	}

	if err != nil {
		sh.Abort()
		return
	}

	if response == "" {
		sh.connection.Send("AUTHENTICATE", "+")
		return
	}

	sh.sendAuthData(response)
}

func (sh *SASLHandler) sendAuthData(data string) {
	if len(data) == 0 {
		sh.connection.Send("AUTHENTICATE", "+")
		return
	}

	if len(data) <= sh.maxChunkSize {
		sh.connection.Send("AUTHENTICATE", data)
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

		sh.connection.Send("AUTHENTICATE", chunk)
	}

	// Send final empty chunk to indicate end of data
	sh.connection.Send("AUTHENTICATE", "+")
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
		sh.eventBus.Emit(&Event{
			Type: EventSASLSuccess,
			Data: &SASLData{
				Mechanism: mechanism.Name(),
				Data:      "authentication successful",
			},
		})
	}
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

	sh.eventBus.Emit(&Event{
		Type: EventSASLFail,
		Data: &SASLData{
			Mechanism: mechanismName,
			Data:      reason,
		},
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
func (sh *SASLHandler) startPlainAuthentication(config *ConnectionConfig) error {
	if !sh.SupportsPlain() {
		return fmt.Errorf("PLAIN mechanism not supported by server")
	}

	if config.SASLUser == "" || config.SASLPass == "" {
		return fmt.Errorf("username and password required for PLAIN authentication")
	}

	mechanism := &PlainMechanism{
		username: config.SASLUser,
		password: config.SASLPass,
	}
	sh.SetMechanism(mechanism)
	return sh.StartAuthentication()
}
