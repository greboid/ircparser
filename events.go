package parser

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Event system constants
const (
	DefaultEventChannelBuffer = 100
	PanicStackBufferSize      = 1024
)

// IRC numeric codes
const (
	NumericWelcome      = "001"
	NumericISupport     = "005"
	NumericSASLSuccess1 = "900"
	NumericSASLSuccess2 = "903"
	NumericSASLFail1    = "901"
	NumericSASLFail2    = "902"
	NumericSASLFail3    = "904"
	NumericSASLFail4    = "905"
	NumericSASLFail5    = "906"
	NumericSASLFail6    = "907"
	NumericSASLFail7    = "908"
)

// WHOIS numeric codes
const (
	NumericWhoisUser     = "311"
	NumericWhoisServer   = "312"
	NumericWhoisOperator = "313"
	NumericWhoisIdle     = "317"
	NumericWhoisEnd      = "318"
	NumericWhoisChannels = "319"
)

// WHO numeric codes
const (
	NumericWhoReply = "352"
	NumericWhoEnd   = "315"
)

// LIST numeric codes
const (
	NumericListStart = "321"
	NumericList      = "322"
	NumericListEnd   = "323"
)

// NAMES numeric codes
const (
	NumericNamesReply = "353"
	NumericNamesEnd   = "366"
)

// CTCP constants
const (
	CTCPDelimiter = '\x01'
	MinCTCPLength = 2
)

// IRCv3 server-time constants
const (
	ServerTimeFormat = "2006-01-02T15:04:05.000Z"
	ServerTimeTag    = "time"
)

type EventType string

const (
	EventConnected    EventType = "connected"
	EventDisconnected EventType = "disconnected"
	EventMessage      EventType = "message"
	EventPing         EventType = "ping"
	EventPong         EventType = "pong"
	EventError        EventType = "error"
	EventCapLS        EventType = "cap_ls"
	EventCapACK       EventType = "cap_ack"
	EventCapNAK       EventType = "cap_nak"
	EventCapDEL       EventType = "cap_del"
	EventCapNEW       EventType = "cap_new"
	EventSASLAuth     EventType = "sasl_auth"
	EventSASLSuccess  EventType = "sasl_success"
	EventSASLFail     EventType = "sasl_fail"
	EventRegistered   EventType = "registered"
	EventNick         EventType = "nick"
	EventJoin         EventType = "join"
	EventPart         EventType = "part"
	EventQuit         EventType = "quit"
	EventKick         EventType = "kick"
	EventMode         EventType = "mode"
	EventTopic        EventType = "topic"
	EventPrivmsg      EventType = "privmsg"
	EventNotice       EventType = "notice"
	EventInvite       EventType = "invite"
	EventWhois        EventType = "whois"
	EventWho          EventType = "who"
	EventList         EventType = "list"
	EventNames        EventType = "names"
	EventCTCP         EventType = "ctcp"
	EventCTCPReply    EventType = "ctcp_reply"
	EventAction       EventType = "action"
	EventNumeric      EventType = "numeric"
	EventRawIncoming  EventType = "raw_incoming"
	EventRawOutgoing  EventType = "raw_outgoing"
	EventUnknown      EventType = "unknown"
)

type Event struct {
	Type    EventType
	Message *Message
	Data    interface{}
	Time    time.Time
}

type EventHandler func(event *Event)

type handlerWrapper struct {
	id      int
	handler EventHandler
}

type EventBus struct {
	handlers map[EventType][]handlerWrapper
	mutex    sync.RWMutex
	ctx      context.Context
	cancel   context.CancelFunc
	eventCh  chan *Event
	wg       sync.WaitGroup
	nextID   int
	idMutex  sync.Mutex
}

func NewEventBus(ctx context.Context) *EventBus {
	slog.Debug("Creating new event bus", "buffer_size", DefaultEventChannelBuffer)
	ctx, cancel := context.WithCancel(ctx)

	bus := &EventBus{
		handlers: make(map[EventType][]handlerWrapper),
		ctx:      ctx,
		cancel:   cancel,
		eventCh:  make(chan *Event, DefaultEventChannelBuffer),
	}

	bus.wg.Add(1)
	go bus.eventLoop()

	slog.Debug("Event bus created successfully")
	return bus
}

func (eb *EventBus) eventLoop() {
	slog.Debug("Starting event bus loop")
	defer eb.wg.Done()
	defer func() {
		slog.Debug("Event bus loop ended")
	}()

	for {
		select {
		case event := <-eb.eventCh:
			eb.dispatchEvent(event)
		case <-eb.ctx.Done():
			return
		}
	}
}

func (eb *EventBus) dispatchEvent(event *Event) {
	eb.mutex.RLock()
	handlers, exists := eb.handlers[event.Type]
	if !exists {
		eb.mutex.RUnlock()
		return
	}

	handlersCopy := make([]handlerWrapper, len(handlers))
	copy(handlersCopy, handlers)
	eb.mutex.RUnlock()

	slog.Debug("Dispatching to handlers", "event_type", event.Type, "handler_count", len(handlersCopy))

	for _, wrapper := range handlersCopy {
		func() {
			defer func() {
				if r := recover(); r != nil {
					buf := make([]byte, PanicStackBufferSize)
					n := runtime.Stack(buf, false)
					slog.Error("Event handler panic", "event_type", event.Type, "handler_id", wrapper.id, "panic", r, "stack_trace", string(buf[:n]))
				}
			}()
			wrapper.handler(event)
		}()
	}
}

func (eb *EventBus) Subscribe(eventType EventType, handler EventHandler) int {
	eb.idMutex.Lock()
	id := eb.nextID
	eb.nextID++
	eb.idMutex.Unlock()

	eb.mutex.Lock()
	defer eb.mutex.Unlock()

	eb.handlers[eventType] = append(eb.handlers[eventType], handlerWrapper{id: id, handler: handler})
	slog.Debug("Event handler subscribed", "event_type", eventType, "handler_id", id, "total_handlers", len(eb.handlers[eventType]))
	return id
}

func (eb *EventBus) UnsubscribeByID(eventType EventType, id int) {
	eb.mutex.Lock()
	defer eb.mutex.Unlock()

	handlers := eb.handlers[eventType]
	for i := len(handlers) - 1; i >= 0; i-- {
		if handlers[i].id == id {
			eb.handlers[eventType] = append(handlers[:i], handlers[i+1:]...)
			slog.Debug("Event handler unsubscribed", "event_type", eventType, "handler_id", id, "remaining_handlers", len(eb.handlers[eventType]))
			break
		}
	}
}

func (eb *EventBus) Emit(event *Event) {
	select {
	case eb.eventCh <- event:
		// Event successfully queued
	case <-eb.ctx.Done():
		// Context cancelled, drop event silently
	default:
		// Channel is full, log warning and attempt to make space
		slog.Warn("EventBus: channel full, dropping oldest event", "event_type", event.Type)
		select {
		case <-eb.eventCh:
			// Dropped one event, try to send new one
			select {
			case eb.eventCh <- event:
			default:
				// Still can't send, drop this event too
				slog.Warn("EventBus: failed to queue event, dropping", "event_type", event.Type)
			}
		default:
			// Couldn't even drop an event, just discard this one
			slog.Warn("EventBus: failed to queue event, dropping", "event_type", event.Type)
		}
	}
}

func (eb *EventBus) EmitSync(event *Event) {
	slog.Debug("Emitting event synchronously", "event_type", event.Type)
	eb.dispatchEvent(event)
}

func (eb *EventBus) Close() {
	slog.Debug("Closing event bus")
	eb.cancel()
	eb.wg.Wait()
	close(eb.eventCh)
	slog.Debug("Event bus closed")
}

func (eb *EventBus) Wait() {
	eb.wg.Wait()
}

func DetermineEventType(msg *Message) []EventType {
	if msg == nil {
		return []EventType{EventUnknown}
	}
	switch msg.Command {
	case "PING":
		return []EventType{EventPing}
	case "PONG":
		return []EventType{EventPong}
	case "ERROR":
		return []EventType{EventError}
	case "NICK":
		return []EventType{EventNick}
	case "JOIN":
		return []EventType{EventJoin}
	case "PART":
		return []EventType{EventPart}
	case "QUIT":
		return []EventType{EventQuit}
	case "KICK":
		return []EventType{EventKick}
	case "MODE":
		return []EventType{EventMode}
	case "TOPIC":
		return []EventType{EventTopic}
	case "PRIVMSG":
		if len(msg.Params) >= 2 && isActionMessage(msg.GetTrailing()) {
			return []EventType{EventAction}
		}
		return []EventType{EventPrivmsg}
	case "NOTICE":
		return []EventType{EventNotice}
	case "INVITE":
		return []EventType{EventInvite}
	case "CAP":
		if len(msg.Params) >= 2 {
			switch msg.Params[1] {
			case "LS":
				return []EventType{EventCapLS}
			case "ACK":
				return []EventType{EventCapACK}
			case "NAK":
				return []EventType{EventCapNAK}
			case "DEL":
				return []EventType{EventCapDEL}
			case "NEW":
				return []EventType{EventCapNEW}
			}
		}
		return []EventType{EventUnknown}
	case "AUTHENTICATE":
		return []EventType{EventSASLAuth}
	case NumericSASLSuccess1, NumericSASLFail1, NumericSASLFail2, NumericSASLSuccess2, NumericSASLFail3, NumericSASLFail4, NumericSASLFail5, NumericSASLFail6, NumericSASLFail7:
		if msg.Command == NumericSASLSuccess1 || msg.Command == NumericSASLSuccess2 {
			return []EventType{EventSASLSuccess}
		}
		return []EventType{EventSASLFail}
	case NumericWelcome:
		return []EventType{EventRegistered, EventNumeric}
	case NumericWhoisUser, NumericWhoisServer, NumericWhoisOperator, NumericWhoisIdle, NumericWhoisEnd, NumericWhoisChannels:
		return []EventType{EventWhois}
	case NumericWhoReply, NumericWhoEnd:
		return []EventType{EventWho}
	case NumericListStart, NumericList, NumericListEnd:
		return []EventType{EventList}
	case NumericNamesReply, NumericNamesEnd:
		return []EventType{EventNames}
	default:
		if msg.IsNumeric() {
			return []EventType{EventNumeric}
		}
		return []EventType{EventUnknown}
	}
}

type ConnectedData struct {
	ServerName string
	UserModes  string
}

type DisconnectedData struct {
	Reason string
	Error  error
}

type PingData struct {
	Server string
}

type PongData struct {
	Server string
}

type ErrorData struct {
	Message string
}

type CapData struct {
	Subcommand string
	Caps       []string
}

type SASLData struct {
	Mechanism string
	Data      string
}

type NickData struct {
	OldNick string
	NewNick string
}

type JoinData struct {
	Channel string
	Nick    string
}

type PartData struct {
	Channel string
	Nick    string
	Reason  string
}

type QuitData struct {
	Nick   string
	Reason string
}

type KickData struct {
	Channel string
	Nick    string
	Kicked  string
	Reason  string
}

type ModeData struct {
	Target string
	Modes  string
	Args   []string
}

type TopicData struct {
	Channel string
	Topic   string
	Nick    string
}

type PrivmsgData struct {
	Target   string
	Nick     string
	Message  string
	IsAction bool
	IsCTCP   bool
}

type NoticeData struct {
	Target   string
	Nick     string
	Message  string
	IsAction bool
	IsCTCP   bool
}

type InviteData struct {
	Channel string
	Nick    string
	Target  string
}

type ActionData struct {
	Target  string
	Nick    string
	Message string
}

type NumericData struct {
	Code   string
	Params []string
}

type RawIncomingData struct {
	Line      string
	Message   *Message
	Timestamp time.Time
}

type RawOutgoingData struct {
	Line      string
	Timestamp time.Time
}

func parseServerTime(msg *Message) time.Time {
	if msg == nil {
		return time.Now().UTC()
	}

	timeTag, exists := msg.Tags[ServerTimeTag]
	if !exists || timeTag == "" {
		return time.Now().UTC()
	}

	parsedTime, err := time.Parse(ServerTimeFormat, timeTag)
	if err != nil {
		slog.Debug("Failed to parse server time tag, using current time", "time_tag", timeTag, "error", err)
		return time.Now().UTC()
	}
	return parsedTime
}

func CreateEventFromMessage(msg *Message) []*Event {
	eventTypes := DetermineEventType(msg)
	events := make([]*Event, 0, len(eventTypes))

	for _, eventType := range eventTypes {
		event := &Event{
			Type:    eventType,
			Message: msg,
			Time:    parseServerTime(msg),
		}

		switch eventType {
		case EventPing:
			event.Data = &PingData{
				Server: msg.GetTrailing(),
			}
		case EventPong:
			event.Data = &PongData{
				Server: msg.GetTrailing(),
			}
		case EventError:
			event.Data = &ErrorData{
				Message: msg.GetTrailing(),
			}
		case EventNick:
			event.Data = &NickData{
				OldNick: extractNick(msg.Source),
				NewNick: msg.GetParam(0),
			}
		case EventJoin:
			event.Data = &JoinData{
				Channel: msg.GetParam(0),
				Nick:    extractNick(msg.Source),
			}
		case EventPart:
			event.Data = &PartData{
				Channel: msg.GetParam(0),
				Nick:    extractNick(msg.Source),
				Reason:  msg.GetTrailing(),
			}
		case EventQuit:
			event.Data = &QuitData{
				Nick:   extractNick(msg.Source),
				Reason: msg.GetTrailing(),
			}
		case EventKick:
			event.Data = &KickData{
				Channel: msg.GetParam(0),
				Nick:    extractNick(msg.Source),
				Kicked:  msg.GetParam(1),
				Reason:  msg.GetTrailing(),
			}
		case EventMode:
			event.Data = &ModeData{
				Target: msg.GetParam(0),
				Modes:  msg.GetParam(1),
				Args:   msg.Params[2:],
			}
		case EventTopic:
			event.Data = &TopicData{
				Channel: msg.GetParam(0),
				Topic:   msg.GetTrailing(),
				Nick:    extractNick(msg.Source),
			}
		case EventPrivmsg:
			message := msg.GetTrailing()
			isCTCP := isValidCTCP(message)
			event.Data = &PrivmsgData{
				Target:  msg.GetParam(0),
				Nick:    extractNick(msg.Source),
				Message: message,
				IsCTCP:  isCTCP,
			}
		case EventNotice:
			message := msg.GetTrailing()
			isCTCP := isValidCTCP(message)
			event.Data = &NoticeData{
				Target:  msg.GetParam(0),
				Nick:    extractNick(msg.Source),
				Message: message,
				IsCTCP:  isCTCP,
			}
		case EventInvite:
			event.Data = &InviteData{
				Channel: msg.GetTrailing(),
				Nick:    extractNick(msg.Source),
				Target:  msg.GetParam(0),
			}
		case EventAction:
			event.Data = &ActionData{
				Target:  msg.GetParam(0),
				Nick:    extractNick(msg.Source),
				Message: msg.GetTrailing(),
			}
		case EventNumeric:
			event.Data = &NumericData{
				Code:   msg.Command,
				Params: msg.Params,
			}
		}

		events = append(events, event)
	}

	return events
}

func extractNick(source string) string {
	if source == "" {
		return ""
	}

	bangIndex := strings.Index(source, "!")
	if bangIndex != -1 {
		return source[:bangIndex]
	}

	return source
}

// isValidCTCP checks if a message is a valid CTCP message (but not ACTION)
func isValidCTCP(message string) bool {
	// Basic length check
	if len(message) < MinCTCPLength {
		return false
	}

	// Check for proper CTCP delimiters
	if message[0] != CTCPDelimiter || message[len(message)-1] != CTCPDelimiter {
		return false
	}

	// Extract the content between delimiters
	content := message[1 : len(message)-1]
	if content == "" {
		return false
	}

	// Check for valid CTCP command format
	parts := strings.Fields(content)
	if len(parts) == 0 {
		return false
	}

	command := strings.ToUpper(parts[0])

	// Exclude ACTION messages - they should be handled separately
	if command == "ACTION" {
		return false
	}

	return command != ""
}

// isActionMessage checks if a message is an ACTION CTCP message
func isActionMessage(message string) bool {
	// Basic length check - ACTION needs at least "\001ACTION\001"
	if len(message) < 9 {
		return false
	}

	// Check for proper CTCP delimiters
	if message[0] != CTCPDelimiter || message[len(message)-1] != CTCPDelimiter {
		return false
	}

	// Check if it starts with ACTION
	content := message[1 : len(message)-1]
	return strings.HasPrefix(content, "ACTION")
}
