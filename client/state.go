package client

import (
	"sync"
	"time"

	"github.com/greboid/ircparser/parser"
)

// UserMode represents a user mode with its character and parameter
type UserMode struct {
	Mode      rune
	Parameter string
}

// ChannelMode represents a channel mode with its character and parameter
type ChannelMode struct {
	Mode      rune
	Parameter string
}

// User represents a user in an IRC channel
type User struct {
	Nick     string
	Username string
	Host     string
	Modes    []UserMode
	mux      sync.RWMutex
}

func NewUser(nick, username, host string) *User {
	return &User{
		Nick:     nick,
		Username: username,
		Host:     host,
		Modes:    make([]UserMode, 0),
	}
}

func (u *User) HasMode(mode rune) bool {
	u.mux.RLock()
	defer u.mux.RUnlock()
	for _, m := range u.Modes {
		if m.Mode == mode {
			return true
		}
	}
	return false
}

func (u *User) AddMode(mode rune, parameter string) {
	u.mux.Lock()
	defer u.mux.Unlock()
	// Remove existing mode if present
	for i, m := range u.Modes {
		if m.Mode == mode {
			u.Modes[i] = UserMode{Mode: mode, Parameter: parameter}
			return
		}
	}
	// Add new mode
	u.Modes = append(u.Modes, UserMode{Mode: mode, Parameter: parameter})
}

func (u *User) RemoveMode(mode rune) {
	u.mux.Lock()
	defer u.mux.Unlock()
	for i, m := range u.Modes {
		if m.Mode == mode {
			u.Modes = append(u.Modes[:i], u.Modes[i+1:]...)
			return
		}
	}
}

func (u *User) GetModes() []UserMode {
	u.mux.RLock()
	defer u.mux.RUnlock()
	modes := make([]UserMode, len(u.Modes))
	copy(modes, u.Modes)
	return modes
}

// Channel represents an IRC channel
type Channel struct {
	Name     string
	Topic    string
	TopicBy  string
	TopicAt  time.Time
	Users    map[string]*User
	Modes    []ChannelMode
	usersMux sync.RWMutex
	modesMux sync.RWMutex
}

func NewChannel(name string) *Channel {
	return &Channel{
		Name:  name,
		Users: make(map[string]*User),
		Modes: make([]ChannelMode, 0),
	}
}

func (c *Channel) AddUser(user *User) {
	c.usersMux.Lock()
	defer c.usersMux.Unlock()
	c.Users[user.Nick] = user
}

func (c *Channel) RemoveUser(nick string) {
	c.usersMux.Lock()
	defer c.usersMux.Unlock()
	delete(c.Users, nick)
}

func (c *Channel) GetUser(nick string) *User {
	c.usersMux.RLock()
	defer c.usersMux.RUnlock()
	return c.Users[nick]
}

func (c *Channel) GetUsers() map[string]*User {
	c.usersMux.RLock()
	defer c.usersMux.RUnlock()
	users := make(map[string]*User, len(c.Users))
	for nick, user := range c.Users {
		users[nick] = user
	}
	return users
}

func (c *Channel) GetUserCount() int {
	c.usersMux.RLock()
	defer c.usersMux.RUnlock()
	return len(c.Users)
}

func (c *Channel) RenameUser(oldNick, newNick string) {
	c.usersMux.Lock()
	defer c.usersMux.Unlock()
	if user, exists := c.Users[oldNick]; exists {
		user.Nick = newNick
		c.Users[newNick] = user
		delete(c.Users, oldNick)
	}
}

func (c *Channel) SetTopic(topic, by string, at time.Time) {
	c.Topic = topic
	c.TopicBy = by
	c.TopicAt = at
}

func (c *Channel) HasMode(mode rune) bool {
	c.modesMux.RLock()
	defer c.modesMux.RUnlock()
	for _, m := range c.Modes {
		if m.Mode == mode {
			return true
		}
	}
	return false
}

func (c *Channel) AddMode(mode rune, parameter string) {
	c.modesMux.Lock()
	defer c.modesMux.Unlock()
	// Remove existing mode if present
	for i, m := range c.Modes {
		if m.Mode == mode {
			c.Modes[i] = ChannelMode{Mode: mode, Parameter: parameter}
			return
		}
	}
	// Add new mode
	c.Modes = append(c.Modes, ChannelMode{Mode: mode, Parameter: parameter})
}

func (c *Channel) RemoveMode(mode rune) {
	c.modesMux.Lock()
	defer c.modesMux.Unlock()
	for i, m := range c.Modes {
		if m.Mode == mode {
			c.Modes = append(c.Modes[:i], c.Modes[i+1:]...)
			return
		}
	}
}

func (c *Channel) GetModes() []ChannelMode {
	c.modesMux.RLock()
	defer c.modesMux.RUnlock()
	modes := make([]ChannelMode, len(c.Modes))
	copy(modes, c.Modes)
	return modes
}

// ServerInfo represents server capabilities and limits
type ServerInfo struct {
	// Basic limits
	MaxLineLength  int
	MaxTopicLength int
	MaxChannels    int
	MaxNickLength  int
	MaxAwayLength  int

	// Channel and user modes
	ChannelModes    map[rune]string // mode -> description
	UserModes       map[rune]string // mode -> description
	ChannelPrefixes map[rune]rune   // prefix -> mode (e.g., '@' -> 'o')

	// Network info
	NetworkName string
	CaseMapping string

	// ISUPPORT data
	ISupport map[string]string

	// IRCv3 capabilities
	Capabilities map[string]string

	mux sync.RWMutex
}

func NewServerInfo() *ServerInfo {
	return &ServerInfo{
		MaxLineLength:   512,
		MaxTopicLength:  390,
		MaxChannels:     10,
		MaxNickLength:   9,
		MaxAwayLength:   160,
		ChannelModes:    make(map[rune]string),
		UserModes:       make(map[rune]string),
		ChannelPrefixes: make(map[rune]rune),
		ISupport:        make(map[string]string),
		Capabilities:    make(map[string]string),
		CaseMapping:     "ascii",
	}
}

func (si *ServerInfo) SetISupport(key, value string) {
	si.mux.Lock()
	defer si.mux.Unlock()
	si.ISupport[key] = value

	// Parse common ISUPPORT values
	switch key {
	case "CHANNELLEN":
		if len(value) > 0 {
			if val := parseInt(value); val > 0 {
				si.MaxLineLength = val
			}
		}
	case "TOPICLEN":
		if len(value) > 0 {
			if val := parseInt(value); val > 0 {
				si.MaxTopicLength = val
			}
		}
	case "CHANLIMIT":
		if len(value) > 0 {
			if val := parseInt(value); val > 0 {
				si.MaxChannels = val
			}
		}
	case "NICKLEN":
		if len(value) > 0 {
			if val := parseInt(value); val > 0 {
				si.MaxNickLength = val
			}
		}
	case "AWAYLEN":
		if len(value) > 0 {
			if val := parseInt(value); val > 0 {
				si.MaxAwayLength = val
			}
		}
	case "NETWORK":
		si.NetworkName = value
	case "CASEMAPPING":
		si.CaseMapping = value
	case "CHANMODES":
		si.parseChannelModes(value)
	case "PREFIX":
		si.parseChannelPrefixes(value)
	}
}

func (si *ServerInfo) GetISupport(key string) (string, bool) {
	si.mux.RLock()
	defer si.mux.RUnlock()
	value, exists := si.ISupport[key]
	return value, exists
}

func (si *ServerInfo) SetCapability(key, value string) {
	si.mux.Lock()
	defer si.mux.Unlock()
	si.Capabilities[key] = value
}

func (si *ServerInfo) RemoveCapability(key string) {
	si.mux.Lock()
	defer si.mux.Unlock()
	delete(si.Capabilities, key)
}

func (si *ServerInfo) HasCapability(key string) bool {
	si.mux.RLock()
	defer si.mux.RUnlock()
	_, exists := si.Capabilities[key]
	return exists
}

func (si *ServerInfo) GetCapabilities() map[string]string {
	si.mux.RLock()
	defer si.mux.RUnlock()
	caps := make(map[string]string, len(si.Capabilities))
	for k, v := range si.Capabilities {
		caps[k] = v
	}
	return caps
}

func (si *ServerInfo) parseChannelModes(value string) {
	// TODO: Parse CHANMODES value (e.g., "b,k,l,imnpst")
	// This would populate the ChannelModes map
}

func (si *ServerInfo) parseChannelPrefixes(value string) {
	// TODO: Parse PREFIX value (e.g., "(ov)@+")
	// This would populate the ChannelPrefixes map
}

// parseInt parses a string to int, returns 0 if invalid
func parseInt(s string) int {
	result := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			result = result*10 + int(r-'0')
		} else {
			return 0
		}
	}
	return result
}

// ClientState represents the complete state of an IRC client
type ClientState struct {
	// Connection state
	ConnectionState parser.ConnectionState

	// User identity
	CurrentNick   string
	PreferredNick string
	Username      string
	Realname      string
	UserModes     []UserMode

	// Server information
	ServerInfo *ServerInfo

	// Channels the user is in
	Channels map[string]*Channel

	// State mutexes
	connMux     sync.RWMutex
	userMux     sync.RWMutex
	channelsMux sync.RWMutex
}

func NewClientState() *ClientState {
	return &ClientState{
		ConnectionState: parser.StateDisconnected,
		ServerInfo:      NewServerInfo(),
		Channels:        make(map[string]*Channel),
		UserModes:       make([]UserMode, 0),
	}
}

// Connection state methods
func (cs *ClientState) SetConnectionState(state parser.ConnectionState) {
	cs.connMux.Lock()
	defer cs.connMux.Unlock()
	cs.ConnectionState = state
}

func (cs *ClientState) GetConnectionState() parser.ConnectionState {
	cs.connMux.RLock()
	defer cs.connMux.RUnlock()
	return cs.ConnectionState
}

func (cs *ClientState) IsConnected() bool {
	state := cs.GetConnectionState()
	return state == parser.StateConnected || state == parser.StateRegistered
}

func (cs *ClientState) IsRegistered() bool {
	return cs.GetConnectionState() == parser.StateRegistered
}

// User identity methods
func (cs *ClientState) SetCurrentNick(nick string) {
	cs.userMux.Lock()
	defer cs.userMux.Unlock()
	cs.CurrentNick = nick
}

func (cs *ClientState) GetCurrentNick() string {
	cs.userMux.RLock()
	defer cs.userMux.RUnlock()
	return cs.CurrentNick
}

func (cs *ClientState) SetPreferredNick(nick string) {
	cs.userMux.Lock()
	defer cs.userMux.Unlock()
	cs.PreferredNick = nick
}

func (cs *ClientState) GetPreferredNick() string {
	cs.userMux.RLock()
	defer cs.userMux.RUnlock()
	return cs.PreferredNick
}

func (cs *ClientState) SetUserInfo(username, realname string) {
	cs.userMux.Lock()
	defer cs.userMux.Unlock()
	cs.Username = username
	cs.Realname = realname
}

func (cs *ClientState) GetUserInfo() (string, string) {
	cs.userMux.RLock()
	defer cs.userMux.RUnlock()
	return cs.Username, cs.Realname
}

func (cs *ClientState) AddUserMode(mode rune, parameter string) {
	cs.userMux.Lock()
	defer cs.userMux.Unlock()
	// Remove existing mode if present
	for i, m := range cs.UserModes {
		if m.Mode == mode {
			cs.UserModes[i] = UserMode{Mode: mode, Parameter: parameter}
			return
		}
	}
	// Add new mode
	cs.UserModes = append(cs.UserModes, UserMode{Mode: mode, Parameter: parameter})
}

func (cs *ClientState) RemoveUserMode(mode rune) {
	cs.userMux.Lock()
	defer cs.userMux.Unlock()
	for i, m := range cs.UserModes {
		if m.Mode == mode {
			cs.UserModes = append(cs.UserModes[:i], cs.UserModes[i+1:]...)
			return
		}
	}
}

func (cs *ClientState) HasUserMode(mode rune) bool {
	cs.userMux.RLock()
	defer cs.userMux.RUnlock()
	for _, m := range cs.UserModes {
		if m.Mode == mode {
			return true
		}
	}
	return false
}

func (cs *ClientState) GetUserModes() []UserMode {
	cs.userMux.RLock()
	defer cs.userMux.RUnlock()
	modes := make([]UserMode, len(cs.UserModes))
	copy(modes, cs.UserModes)
	return modes
}

// Channel methods
func (cs *ClientState) AddChannel(channel *Channel) {
	cs.channelsMux.Lock()
	defer cs.channelsMux.Unlock()
	cs.Channels[channel.Name] = channel
}

func (cs *ClientState) RemoveChannel(name string) {
	cs.channelsMux.Lock()
	defer cs.channelsMux.Unlock()
	delete(cs.Channels, name)
}

func (cs *ClientState) GetChannel(name string) *Channel {
	cs.channelsMux.RLock()
	defer cs.channelsMux.RUnlock()
	return cs.Channels[name]
}

func (cs *ClientState) GetChannels() map[string]*Channel {
	cs.channelsMux.RLock()
	defer cs.channelsMux.RUnlock()
	channels := make(map[string]*Channel, len(cs.Channels))
	for name, channel := range cs.Channels {
		channels[name] = channel
	}
	return channels
}

func (cs *ClientState) GetChannelNames() []string {
	cs.channelsMux.RLock()
	defer cs.channelsMux.RUnlock()
	names := make([]string, 0, len(cs.Channels))
	for name := range cs.Channels {
		names = append(names, name)
	}
	return names
}

func (cs *ClientState) IsInChannel(name string) bool {
	cs.channelsMux.RLock()
	defer cs.channelsMux.RUnlock()
	_, exists := cs.Channels[name]
	return exists
}

// Clear all state (for disconnect/reset)
func (cs *ClientState) Reset() {
	cs.connMux.Lock()
	cs.userMux.Lock()
	cs.channelsMux.Lock()
	defer cs.connMux.Unlock()
	defer cs.userMux.Unlock()
	defer cs.channelsMux.Unlock()

	cs.ConnectionState = parser.StateDisconnected
	cs.CurrentNick = ""
	cs.UserModes = cs.UserModes[:0]
	cs.Channels = make(map[string]*Channel)
	cs.ServerInfo = NewServerInfo()
}
