package parser

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type PingHandler struct {
	connection   *Connection
	eventBus     *EventBus
	ctx          context.Context
	cancel       context.CancelFunc
	lastResponse time.Time
	pingTimer    *time.Timer
}

func NewPingHandler(ctx context.Context, connection *Connection, eventBus *EventBus) *PingHandler {
	ctx, cancel := context.WithCancel(ctx)

	h := &PingHandler{
		connection: connection,
		eventBus:   eventBus,
		ctx:        ctx,
		cancel:     cancel,
	}

	eventBus.Subscribe(EventPing, h.HandlePing)
	eventBus.Subscribe(EventRawIncoming, h.HandleRaw)
	eventBus.Subscribe(EventRegistered, h.HandleWelcome)
	return h
}

func (h *PingHandler) HandleWelcome(*Event) {
	if h.pingTimer == nil {
		h.pingTimer = time.AfterFunc(DefaultPingTimeout, h.SendPing)
	}
}

func (h *PingHandler) SendPing() {
	err := h.connection.Send("PING", fmt.Sprintf("tithon-%s", time.Now().Format("060102150405")))
	if err != nil {
		slog.Error("Failed to send PONG response", "error", err)
	}
}

func (h *PingHandler) HandleRaw(*Event) {
	h.lastResponse = time.Now()
	if h.pingTimer == nil {
		return
	}
	h.pingTimer.Reset(DefaultPingTimeout)
}

func (h *PingHandler) HandlePing(event *Event) {
	pingData, ok := event.Data.(*PingData)
	if !ok {
		return
	}
	err := h.connection.Send("PONG", pingData.Server)
	if err != nil {
		slog.Error("Failed to send PONG response", "error", err, "server", pingData.Server)
	}
}
