package parser

import (
	"context"
	"log/slog"
	"sync"
)

type Parser struct {
	connection   *Connection
	eventBus     *EventBus
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	capabilities map[string]string
}

func NewParser(ctx context.Context, config *ConnectionConfig) *Parser {
	slog.Debug("Creating new parser", "host", config.Host(), "port", config.Port(), "tls", config.TLS())
	ctx, cancel := context.WithCancel(ctx)

	eventBus := NewEventBus(ctx)
	connection := NewConnection(ctx, config, eventBus)

	parser := &Parser{
		connection:   connection,
		eventBus:     eventBus,
		ctx:          ctx,
		cancel:       cancel,
		capabilities: make(map[string]string),
	}

	SetupDefaultHandlers(ctx, connection, eventBus, config, parser.capabilities)

	slog.Debug("Parser created successfully")
	return parser
}

func (p *Parser) Start() error {
	slog.Debug("Parser starting")

	err := p.connection.Connect()
	if err != nil {
		slog.Error("Failed to connect", "error", err)
		return NewConnectionError("Start", "failed to establish connection", err)
	}

	slog.Info("Parser started successfully")
	return nil
}

func (p *Parser) Stop() {
	slog.Debug("Stopping parser")
	p.connection.Disconnect()
	p.cancel()
	p.wg.Wait()
	p.eventBus.Close()
	slog.Info("Parser stopped successfully")
}

func (p *Parser) IsConnected() bool {
	return p.connection.IsConnected()
}

func (p *Parser) IsRegistered() bool {
	return p.connection.IsRegistered()
}

func (p *Parser) GetConnection() *Connection {
	return p.connection
}

func (p *Parser) GetEventBus() *EventBus {
	return p.eventBus
}

func (p *Parser) SendRaw(line string) error {
	err := p.connection.SendRaw(line)
	if err != nil {
		slog.Error("Failed to send raw line", "line", line, "error", err)
	}
	return err
}

func (p *Parser) Send(command string, params ...string) error {
	slog.Debug("Sending command", "command", command, "params", params)
	err := p.connection.Send(command, params...)
	if err != nil {
		slog.Error("Failed to send command", "command", command, "params", params, "error", err)
	}
	return err
}

func (p *Parser) Join(channel string) error {
	if !p.connection.IsRegistered() {
		slog.Warn("Join attempted but not registered", "channel", channel)
		return NewRegistrationError("Join", "client not registered with server")
	}
	slog.Debug("Joining channel", "channel", channel)
	err := p.connection.Send("JOIN", channel)
	if err != nil {
		slog.Error("Failed to join channel", "channel", channel, "error", err)
	}
	return err
}

func (p *Parser) Part(channel string, reason string) error {
	if !p.connection.IsRegistered() {
		slog.Warn("Part attempted but not registered", "channel", channel)
		return NewRegistrationError("Part", "client not registered with server")
	}
	slog.Debug("Parting channel", "channel", channel, "reason", reason)
	var err error
	if reason != "" {
		err = p.connection.Send("PART", channel, reason)
	} else {
		err = p.connection.Send("PART", channel)
	}
	if err != nil {
		slog.Error("Failed to part channel", "channel", channel, "reason", reason, "error", err)
	}
	return err
}

func (p *Parser) Privmsg(target, message string) error {
	if !p.connection.IsRegistered() {
		slog.Warn("Privmsg attempted but not registered", "target", target)
		return NewRegistrationError("Privmsg", "client not registered with server")
	}
	slog.Debug("Sending privmsg", "target", target, "message_length", len(message))
	err := p.connection.Send("PRIVMSG", target, message)
	if err != nil {
		slog.Error("Failed to send privmsg", "target", target, "error", err)
		return err
	}

	if _, ok := p.capabilities["echo-message"]; !ok {
		p.generateFakeEcho("PRIVMSG", target, message)
	}

	return err
}

func (p *Parser) Notice(target, message string) error {
	if !p.connection.IsRegistered() {
		slog.Warn("Notice attempted but not registered", "target", target)
		return NewRegistrationError("Notice", "client not registered with server")
	}
	slog.Debug("Sending notice", "target", target, "message_length", len(message))
	err := p.connection.Send("NOTICE", target, message)
	if err != nil {
		slog.Error("Failed to send notice", "target", target, "error", err)
		return err
	}
	if _, ok := p.capabilities["echo-message"]; !ok {
		p.generateFakeEcho("NOTICE", target, message)
	}

	return err
}

func (p *Parser) generateFakeEcho(command, target, message string) {
	currentNick := p.connection.GetCurrentNick()
	fakeMsg := &Message{
		Source:  currentNick + "!" + currentNick + "@" + p.connection.GetConfig().Host(),
		Command: command,
		Params:  []string{target, message},
		Tags:    make(Tags),
		Raw:     "",
	}
	events := CreateEventFromMessage(fakeMsg)
	for _, event := range events {
		if event != nil {
			slog.Debug("Generated fake echo event", "command", command, "target", target, "echo_message_available", false)
			p.eventBus.Emit(event)
		}
	}
}

func (p *Parser) Nick(nick string) error {
	slog.Debug("Changing nick", "nick", nick)
	err := p.connection.Send("NICK", nick)
	if err != nil {
		slog.Error("Failed to change nick", "nick", nick, "error", err)
	}
	return err
}

func (p *Parser) Quit(reason string) error {
	slog.Debug("Quitting", "reason", reason)
	var err error
	if reason != "" {
		err = p.connection.Send("QUIT", reason)
	} else {
		err = p.connection.Send("QUIT")
	}
	if err != nil {
		slog.Error("Failed to quit", "reason", reason, "error", err)
	}
	return err
}

func (p *Parser) Mode(target, modes string, args ...string) error {
	if !p.connection.IsRegistered() {
		slog.Warn("Mode attempted but not registered", "target", target, "modes", modes)
		return NewRegistrationError("Mode", "client not registered with server")
	}
	slog.Debug("Setting mode", "target", target, "modes", modes, "args", args)
	params := []string{target, modes}
	params = append(params, args...)
	err := p.connection.Send("MODE", params...)
	if err != nil {
		slog.Error("Failed to set mode", "target", target, "modes", modes, "args", args, "error", err)
	}
	return err
}

func (p *Parser) Topic(channel, topic string) error {
	if !p.connection.IsRegistered() {
		return NewRegistrationError("Topic", "client not registered with server")
	}
	if topic != "" {
		return p.connection.Send("TOPIC", channel, topic)
	}
	return p.connection.Send("TOPIC", channel)
}

func (p *Parser) Kick(channel, nick, reason string) error {
	if !p.connection.IsRegistered() {
		return NewRegistrationError("Kick", "client not registered with server")
	}
	if reason != "" {
		return p.connection.Send("KICK", channel, nick, reason)
	}
	return p.connection.Send("KICK", channel, nick)
}

func (p *Parser) Invite(nick, channel string) error {
	if !p.connection.IsRegistered() {
		return NewRegistrationError("Invite", "client not registered with server")
	}
	return p.connection.Send("INVITE", nick, channel)
}

func (p *Parser) Whois(nick string) error {
	if !p.connection.IsRegistered() {
		return NewRegistrationError("Whois", "client not registered with server")
	}
	return p.connection.Send("WHOIS", nick)
}

func (p *Parser) Who(target string) error {
	if !p.connection.IsRegistered() {
		return NewRegistrationError("Who", "client not registered with server")
	}
	return p.connection.Send("WHO", target)
}

func (p *Parser) List(channels ...string) error {
	if !p.connection.IsRegistered() {
		return NewRegistrationError("List", "client not registered with server")
	}
	if len(channels) > 0 {
		return p.connection.Send("LIST", channels[0])
	}
	return p.connection.Send("LIST")
}

func (p *Parser) Names(channels ...string) error {
	if !p.connection.IsRegistered() {
		return NewRegistrationError("Names", "client not registered with server")
	}
	if len(channels) > 0 {
		return p.connection.Send("NAMES", channels[0])
	}
	return p.connection.Send("NAMES")
}

func (p *Parser) GetCurrentNick() string {
	return p.connection.GetCurrentNick()
}

func (p *Parser) GetISupport() map[string]string {
	return p.connection.GetISupport()
}

func (p *Parser) GetISupportValue(key string) string {
	return p.connection.GetISupportValue(key)
}

func (p *Parser) GetServerName() string {
	return p.connection.GetServerName()
}

func (p *Parser) GetState() ConnectionState {
	return p.connection.GetState()
}

func (p *Parser) Subscribe(eventType EventType, handler EventHandler) int {
	return p.eventBus.Subscribe(eventType, handler)
}

func (p *Parser) UnsubscribeByID(eventType EventType, id int) {
	p.eventBus.UnsubscribeByID(eventType, id)
}

func (p *Parser) EmitEvent(event *Event) {
	p.eventBus.Emit(event)
}

func (p *Parser) Wait() {
	p.wg.Wait()
	p.connection.Wait()
}
