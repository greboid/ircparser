package parser

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type PingHandler struct {
	send         func(command string, params ...string) error
	emit         func(event *Event)
	ctx          context.Context
	cancel       context.CancelFunc
	lastResponse time.Time
	pingTimer    *time.Timer
}

func NewPingHandler(ctx context.Context, send func(command string, params ...string) error, subscribe func(EventType, EventHandler) int, emit func(event *Event)) *PingHandler {
	ctx, cancel := context.WithCancel(ctx)

	h := &PingHandler{
		send:   send,
		emit:   emit,
		ctx:    ctx,
		cancel: cancel,
	}

	subscribe(EventPing, h.HandlePing)
	subscribe(EventRawIncoming, h.HandleRaw)
	subscribe(EventRegistered, h.HandleWelcome)
	return h
}

func (h *PingHandler) HandleWelcome(*Event) {
	if h.pingTimer == nil {
		h.pingTimer = time.AfterFunc(DefaultPingTimeout, h.SendPing)
	}
}

func (h *PingHandler) SendPing() {
	err := h.send("PING", fmt.Sprintf("tithon-%s", time.Now().Format("060102150405")))
	if err != nil {
		slog.Error("Failed to send PING", "error", err)
		h.emit(&Event{
			Type: EventError,
			Data: &ErrorData{
				Message: fmt.Sprintf("Failed to send ping: %s", err),
			},
		})
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
	err := h.send("PONG", pingData.Server)
	if err != nil {
		slog.Error("Failed to send PONG response", "error", err, "server", pingData.Server)
		h.emit(&Event{
			Type: EventError,
			Data: &ErrorData{
				Message: fmt.Sprintf("Failed to send ping: %s", err),
			},
		})
	}
}
