package client

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/greboid/ircparser/parser"
)

// Client provides a high-level IRC client interface with comprehensive state tracking
type Client struct {
	parser *parser.Parser
	state  *State
	ctx    context.Context
	cancel context.CancelFunc
}

// NewClient creates a new IRC client with state tracking
func NewClient(ctx context.Context, config *parser.ConnectionConfig) *Client {
	ctx, cancel := context.WithCancel(ctx)

	client := &Client{
		state:  NewClientState(),
		ctx:    ctx,
		cancel: cancel,
	}

	// Create the underlying parser
	client.parser = parser.NewParser(ctx, config)

	// Set up initial user info
	client.state.SetPreferredNick(config.Nick())
	client.state.SetCurrentNick(config.Nick())
	client.state.SetUserInfo(config.Username(), config.Realname())

	// Subscribe to events for state tracking
	client.setupEventHandlers()

	slog.Debug("IRC client created", "preferred_nick", config.Nick(), "host", config.Host())
	return client
}

// Start connects to the IRC server and begins the connection process
func (c *Client) Start() error {
	slog.Info("Starting IRC client connection")
	return c.parser.Start()
}

// Stop disconnects from the IRC server and cleans up resources
func (c *Client) Stop() {
	slog.Info("Stopping IRC client")
	c.parser.Stop()
	c.cancel()
}

func (c *Client) IsConnected() bool {
	return c.state.IsConnected()
}

func (c *Client) IsRegistered() bool {
	return c.state.IsRegistered()
}

func (c *Client) GetConnectionState() parser.ConnectionState {
	return c.state.GetConnectionState()
}

func (c *Client) GetCurrentNick() string {
	return c.state.GetCurrentNick()
}

func (c *Client) GetPreferredNick() string {
	return c.state.GetPreferredNick()
}

func (c *Client) GetUserInfo() (username, realname string) {
	return c.state.GetUserInfo()
}

func (c *Client) GetUserModes() []parser.UserMode {
	return c.state.GetUserModes()
}

func (c *Client) HasUserMode(mode rune) bool {
	return c.state.HasUserMode(mode)
}

func (c *Client) GetChannels() map[string]*parser.Channel {
	return c.state.GetChannels()
}

func (c *Client) GetChannelNames() []string {
	return c.state.GetChannelNames()
}

func (c *Client) GetChannel(name string) *parser.Channel {
	return c.state.GetChannel(name)
}

func (c *Client) IsInChannel(name string) bool {
	return c.state.IsInChannel(name)
}

func (c *Client) GetServerInfo() *ServerInfo {
	return c.state.ServerInfo
}

func (c *Client) GetMaxLineLength() int {
	return c.state.ServerInfo.MaxLineLength
}

func (c *Client) GetMaxTopicLength() int {
	return c.state.ServerInfo.MaxTopicLength
}

func (c *Client) GetMaxChannels() int {
	// Check if CHANLIMIT provides specific limits per channel type
	if len(c.state.ServerInfo.ChannelLimits) > 0 {
		total := 0
		for _, limit := range c.state.ServerInfo.ChannelLimits {
			total += limit
		}
		return total
	}

	// Fall back to general MaxChannels limit
	return c.state.ServerInfo.MaxChannels
}

func (c *Client) GetMaxChannelsForType(channelPrefix rune) int {
	// Check if CHANLIMIT provides specific limit for this channel type
	if limit, exists := c.state.ServerInfo.ChannelLimits[channelPrefix]; exists {
		return limit
	}

	// Fall back to general MaxChannels limit
	return c.state.ServerInfo.MaxChannels
}

func (c *Client) GetChannelModes() map[rune]string {
	return c.state.ServerInfo.ChannelModes
}

func (c *Client) GetUserModeMap() map[rune]string {
	return c.state.ServerInfo.UserModes
}

func (c *Client) GetChannelPrefixes() map[rune]rune {
	return c.state.ServerInfo.ChannelPrefixes
}

func (c *Client) GetNetworkName() string {
	return c.state.ServerInfo.NetworkName
}

func (c *Client) GetISupport() map[string]string {
	return c.state.ServerInfo.ISupport
}

func (c *Client) GetISupportValue(key string) (string, bool) {
	return c.state.ServerInfo.GetISupport(key)
}

func (c *Client) GetCapabilities() map[string]string {
	return c.state.ServerInfo.GetCapabilities()
}

func (c *Client) HasCapability(cap string) bool {
	return c.state.ServerInfo.HasCapability(cap)
}

func (c *Client) Join(channel string) error {
	return c.parser.Join(channel)
}

func (c *Client) Part(channel, reason string) error {
	return c.parser.Part(channel, reason)
}

func (c *Client) Privmsg(target, message string) error {
	return c.parser.Privmsg(target, message)
}

func (c *Client) Notice(target, message string) error {
	return c.parser.Notice(target, message)
}

func (c *Client) Nick(nick string) error {
	return c.parser.Nick(nick)
}

func (c *Client) Quit(reason string) error {
	return c.parser.Quit(reason)
}

func (c *Client) Mode(target, modes string, args ...string) error {
	return c.parser.Mode(target, modes, args...)
}

func (c *Client) Topic(channel, topic string) error {
	return c.parser.Topic(channel, topic)
}

func (c *Client) Kick(channel, nick, reason string) error {
	return c.parser.Kick(channel, nick, reason)
}

func (c *Client) Invite(nick, channel string) error {
	return c.parser.Invite(nick, channel)
}

func (c *Client) Whois(nick string) error {
	return c.parser.Whois(nick)
}

func (c *Client) Who(target string) error {
	return c.parser.Who(target)
}

func (c *Client) List(channels ...string) error {
	return c.parser.List(channels...)
}

func (c *Client) Names(channels ...string) error {
	return c.parser.Names(channels...)
}

func (c *Client) SendRaw(line string) error {
	return c.parser.SendRaw(line)
}

func (c *Client) Send(command string, params ...string) error {
	return c.parser.Send(command, params...)
}

func (c *Client) Subscribe(eventType parser.EventType, handler parser.EventHandler) int {
	return c.parser.Subscribe(eventType, handler)
}

func (c *Client) UnsubscribeByID(eventType parser.EventType, id int) {
	c.parser.UnsubscribeByID(eventType, id)
}

func (c *Client) Wait() {
	c.parser.Wait()
}

func (c *Client) emitEvent(event *parser.Event) {
	c.parser.EmitEvent(event)
}

func (c *Client) setupEventHandlers() {
	c.parser.Subscribe(parser.EventConnected, c.handleConnected)
	c.parser.Subscribe(parser.EventDisconnected, c.handleDisconnected)
	c.parser.Subscribe(parser.EventRegistered, c.handleRegistered)

	c.parser.Subscribe(parser.EventNick, c.handleNick)
	c.parser.Subscribe(parser.EventMode, c.handleMode)

	c.parser.Subscribe(parser.EventJoin, c.handleJoin)
	c.parser.Subscribe(parser.EventPart, c.handlePart)
	c.parser.Subscribe(parser.EventQuit, c.handleQuit)
	c.parser.Subscribe(parser.EventKick, c.handleKick)
	c.parser.Subscribe(parser.EventTopic, c.handleTopic)

	c.parser.Subscribe(parser.EventNumeric, c.handleNumeric)
	c.parser.Subscribe(parser.EventCapAdded, c.handleCapAdded)
	c.parser.Subscribe(parser.EventCapRemoved, c.handleCapRemoved)
}

func (c *Client) handleConnected(*parser.Event) {
	slog.Debug("Client: Connected to server")
	c.state.SetConnectionState(parser.StateConnected)
}

func (c *Client) handleDisconnected(*parser.Event) {
	slog.Debug("Client: Disconnected from server")
	c.state.SetConnectionState(parser.StateDisconnected)
	c.state.channelsMux.Lock()
	c.state.Channels = make(map[string]*parser.Channel)
	c.state.channelsMux.Unlock()
}

func (c *Client) handleRegistered(*parser.Event) {
	slog.Debug("Client: Registration completed")
	c.state.SetConnectionState(parser.StateRegistered)
}

func (c *Client) handleNick(event *parser.Event) {
	nickData, ok := event.Data.(*parser.NickData)
	if !ok {
		return
	}

	slog.Debug("Client: Nick change", "old_nick", nickData.OldNick, "new_nick", nickData.NewNick)

	if nickData.OldNick == c.state.GetCurrentNick() {
		c.state.SetCurrentNick(nickData.NewNick)
	}

	for _, channel := range c.state.GetChannels() {
		channel.RenameUser(nickData.OldNick, nickData.NewNick)
	}
}

func (c *Client) handleMode(event *parser.Event) {
	modeData, ok := event.Data.(*parser.ModeData)
	if !ok {
		return
	}

	slog.Debug("Client: Mode change", "target", modeData.Target, "modes", modeData.Modes, "args", modeData.Args)

	adding := true
	argIndex := 0

	for _, char := range modeData.Modes {
		switch char {
		case '+':
			adding = true
		case '-':
			adding = false
		default:
			var parameter string
			if argIndex < len(modeData.Args) {
				parameter = modeData.Args[argIndex]
				argIndex++
			}

			if strings.HasPrefix(modeData.Target, "#") || strings.HasPrefix(modeData.Target, "&") {
				channel := c.state.GetChannel(modeData.Target)
				if channel != nil {
					if adding {
						channel.AddMode(char, parameter)
						if parameter != "" {
							user := channel.GetUser(parameter)
							if user != nil {
								user.AddMode(char, "")
							}
						}
					} else {
						channel.RemoveMode(char)
						if parameter != "" {
							user := channel.GetUser(parameter)
							if user != nil {
								user.RemoveMode(char)
							}
						}
					}
				}
			} else if modeData.Target == c.state.GetCurrentNick() {
				if adding {
					c.state.AddUserMode(char, parameter)
				} else {
					c.state.RemoveUserMode(char)
				}
			}
		}
	}
}

func (c *Client) handleJoin(event *parser.Event) {
	joinData, ok := event.Data.(*parser.JoinData)
	if !ok {
		return
	}

	slog.Debug("Client: User joined channel", "nick", joinData.Nick, "channel", joinData.Channel)

	if joinData.Nick == c.state.GetCurrentNick() {
		channel := parser.NewChannel(joinData.Channel)
		c.state.AddChannel(channel)

		user := parser.NewUser(joinData.Nick, "", "")
		channel.AddUser(user)
	} else {
		channel := c.state.GetChannel(joinData.Channel)
		if channel != nil {
			user := parser.NewUser(joinData.Nick, "", "")
			channel.AddUser(user)
		}
	}
}

func (c *Client) handlePart(event *parser.Event) {
	partData, ok := event.Data.(*parser.PartData)
	if !ok {
		return
	}

	slog.Debug("Client: User parted channel", "nick", partData.Nick, "channel", partData.Channel, "reason", partData.Reason)

	if partData.Nick == c.state.GetCurrentNick() {
		c.state.RemoveChannel(partData.Channel)
	} else {
		channel := c.state.GetChannel(partData.Channel)
		if channel != nil {
			channel.RemoveUser(partData.Nick)
		}
	}
}

func (c *Client) handleQuit(event *parser.Event) {
	quitData, ok := event.Data.(*parser.QuitData)
	if !ok {
		return
	}

	slog.Debug("Client: User quit", "nick", quitData.Nick, "reason", quitData.Reason)

	for _, channel := range c.state.GetChannels() {
		channel.RemoveUser(quitData.Nick)
	}
}

func (c *Client) handleKick(event *parser.Event) {
	kickData, ok := event.Data.(*parser.KickData)
	if !ok {
		return
	}

	slog.Debug("Client: User kicked from channel", "kicked", kickData.Kicked, "channel", kickData.Channel, "by", kickData.Nick, "reason", kickData.Reason)

	if kickData.Kicked == c.state.GetCurrentNick() {
		c.state.RemoveChannel(kickData.Channel)
	} else {
		channel := c.state.GetChannel(kickData.Channel)
		if channel != nil {
			channel.RemoveUser(kickData.Kicked)
		}
	}
}

func (c *Client) handleTopic(event *parser.Event) {
	topicData, ok := event.Data.(*parser.TopicData)
	if !ok {
		return
	}

	slog.Debug("Client: Topic changed", "channel", topicData.Channel, "topic", topicData.Topic, "by", topicData.Nick)

	channel := c.state.GetChannel(topicData.Channel)
	if channel != nil {
		channel.SetTopic(topicData.Topic, topicData.Nick, time.Now())
	}
}

func (c *Client) handleNames(event *parser.Event) {
	numericData, ok := event.Data.(*parser.NumericData)
	if !ok || numericData.Code != parser.NumericNamesReply {
		return
	}

	if len(numericData.Params) < 4 {
		return
	}

	channelName := numericData.Params[2]
	nicks := strings.Fields(numericData.Params[3])

	channel := c.state.GetChannel(channelName)
	if channel == nil {
		channel = parser.NewChannel(channelName)
		c.state.AddChannel(channel)
	}

	slog.Debug("Client: NAMES reply", "channel", channelName, "users", len(nicks))

	for _, nick := range nicks {
		cleanNick := nick
		var userModes []rune

		for len(cleanNick) > 0 {
			firstChar := rune(cleanNick[0])
			if mode, exists := c.state.ServerInfo.ChannelPrefixes[firstChar]; exists {
				userModes = append(userModes, mode)
				cleanNick = cleanNick[1:]
			} else {
				break
			}
		}

		if cleanNick == "" {
			continue
		}

		user := parser.NewUser(cleanNick, "", "")

		for _, mode := range userModes {
			user.AddMode(mode, "")
		}

		channel.AddUser(user)
	}
}

func (c *Client) handleNamesEnd(event *parser.Event) {
	numericData, ok := event.Data.(*parser.NumericData)
	if !ok {
		slog.Debug("Client: handleNamesEnd data not NumericData", "event_type", event.Type, "data_type", fmt.Sprintf("%T", event.Data))
		return
	}
	if numericData.Code != parser.NumericNamesEnd {
		slog.Debug("Client: handleNamesEnd wrong code", "code", numericData.Code, "expected", parser.NumericNamesEnd)
		return
	}

	if len(numericData.Params) < 2 {
		slog.Debug("Client: handleNamesEnd insufficient params", "params", numericData.Params)
		return
	}

	channelName := numericData.Params[1]
	channel := c.state.GetChannel(channelName)
	if channel != nil {
		slog.Debug("Client: NAMES complete", "channel", channelName, "user_count", channel.GetUserCount())

		slog.Debug("Client: About to emit EventNamesComplete", "channel", channelName, "user_count", channel.GetUserCount())

		namesCompleteEvent := &parser.Event{
			Type:    parser.EventNamesComplete,
			Message: event.Message,
			Data: &parser.NamesCompleteData{
				Channel: channel,
			},
			Time: event.Time,
		}

		c.emitEvent(namesCompleteEvent)
		slog.Debug("Client: EventNamesComplete emitted")
	} else {
		slog.Debug("Client: handleNamesEnd channel not found", "channel", channelName)
	}
}

func (c *Client) handleNumeric(event *parser.Event) {
	numericData, ok := event.Data.(*parser.NumericData)
	if !ok {
		return
	}

	slog.Debug("Client: handleNumeric called", "code", numericData.Code)

	switch numericData.Code {
	case parser.NumericISupport:
		for i := 1; i < len(numericData.Params)-1; i++ {
			param := numericData.Params[i]
			if strings.Contains(param, "=") {
				parts := strings.SplitN(param, "=", 2)
				c.state.ServerInfo.SetISupport(parts[0], parts[1])
			} else {
				c.state.ServerInfo.SetISupport(param, "")
			}
		}
		slog.Debug("Client: Updated ISUPPORT", "feature_count", len(c.state.ServerInfo.ISupport))

	case parser.NumericTopic:
		if len(numericData.Params) >= 3 {
			channelName := numericData.Params[1]
			topic := numericData.Params[2]
			channel := c.state.GetChannel(channelName)
			if channel != nil {
				channel.SetTopic(topic, "", time.Time{})
			}
		}

	case parser.NumericTopicInfo:
		if len(numericData.Params) >= 4 {
			channelName := numericData.Params[1]
			topicBy := numericData.Params[2]
			channel := c.state.GetChannel(channelName)
			if channel != nil {
				channel.TopicBy = topicBy
			}
		}

	case parser.NumericNamesReply:
		c.handleNames(event)

	case parser.NumericNamesEnd:
		c.handleNamesEnd(event)
	}
}

func (c *Client) handleCapAdded(event *parser.Event) {
	capData, ok := event.Data.(*parser.CapAddedData)
	if !ok {
		return
	}

	slog.Debug("Client: Capability added", "capability", capData.Capability)
	c.state.ServerInfo.SetCapability(capData.Capability, "")
}

func (c *Client) handleCapRemoved(event *parser.Event) {
	capData, ok := event.Data.(*parser.CapRemovedData)
	if !ok {
		return
	}

	slog.Debug("Client: Capability removed", "capability", capData.Capability)
	c.state.ServerInfo.RemoveCapability(capData.Capability)
}
