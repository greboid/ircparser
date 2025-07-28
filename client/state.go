package client

import (
	"strconv"
	"strings"
	"sync"

	"github.com/greboid/ircparser/parser"
)

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
	// Parse CHANMODES value (e.g., "b,k,l,imnpst")
	// Format: A,B,C,D where:
	// A = modes that add/remove items to/from lists (e.g., +b nick!user@host)
	// B = modes that require a parameter when set/unset (e.g., +k password)
	// C = modes that require a parameter when set only (e.g., +l limit)
	// D = modes that never require a parameter (e.g., +i, +m, +n, +p, +s, +t)

	groups := strings.Split(value, ",")
	if len(groups) != 4 {
		return // Invalid CHANMODES format
	}

	// Type A: List modes (require parameter for both + and -)
	for _, mode := range groups[0] {
		si.ChannelModes[mode] = "list"
	}

	// Type B: Always parameter modes
	for _, mode := range groups[1] {
		si.ChannelModes[mode] = "always_param"
	}

	// Type C: Set-only parameter modes
	for _, mode := range groups[2] {
		si.ChannelModes[mode] = "set_param"
	}

	// Type D: Never parameter modes
	for _, mode := range groups[3] {
		si.ChannelModes[mode] = "never_param"
	}
}

func (si *ServerInfo) parseChannelPrefixes(value string) {
	// Parse PREFIX value (e.g., "(ov)@+")
	// Format: (modes)prefixes where modes and prefixes are same length
	// Each mode corresponds to the prefix at the same position

	if len(value) == 0 {
		return
	}

	// Find the closing parenthesis
	parenIndex := strings.Index(value, ")")
	if parenIndex == -1 || !strings.HasPrefix(value, "(") {
		return // Invalid PREFIX format
	}

	modes := value[1:parenIndex]     // Extract modes between parentheses
	prefixes := value[parenIndex+1:] // Extract prefixes after parentheses

	// Modes and prefixes must have same length
	if len(modes) != len(prefixes) {
		return
	}

	// Map each prefix to its corresponding mode
	for i := 0; i < len(modes); i++ {
		si.ChannelPrefixes[rune(prefixes[i])] = rune(modes[i])
	}
}

// parseInt parses a string to int, returns 0 if invalid
func parseInt(s string) int {
	if val, err := strconv.Atoi(s); err == nil {
		return val
	}
	return 0
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
	UserModes     []parser.UserMode

	// Server information
	ServerInfo *ServerInfo

	// Channels the user is in
	Channels map[string]*parser.Channel

	// State mutexes
	connMux     sync.RWMutex
	userMux     sync.RWMutex
	channelsMux sync.RWMutex
}

func NewClientState() *ClientState {
	return &ClientState{
		ConnectionState: parser.StateDisconnected,
		ServerInfo:      NewServerInfo(),
		Channels:        make(map[string]*parser.Channel),
		UserModes:       make([]parser.UserMode, 0),
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
			cs.UserModes[i] = parser.UserMode{Mode: mode, Parameter: parameter}
			return
		}
	}
	// Add new mode
	cs.UserModes = append(cs.UserModes, parser.UserMode{Mode: mode, Parameter: parameter})
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

func (cs *ClientState) GetUserModes() []parser.UserMode {
	cs.userMux.RLock()
	defer cs.userMux.RUnlock()
	modes := make([]parser.UserMode, len(cs.UserModes))
	copy(modes, cs.UserModes)
	return modes
}

// Channel methods
func (cs *ClientState) AddChannel(channel *parser.Channel) {
	cs.channelsMux.Lock()
	defer cs.channelsMux.Unlock()
	cs.Channels[channel.Name] = channel
}

func (cs *ClientState) RemoveChannel(name string) {
	cs.channelsMux.Lock()
	defer cs.channelsMux.Unlock()
	delete(cs.Channels, name)
}

func (cs *ClientState) GetChannel(name string) *parser.Channel {
	cs.channelsMux.RLock()
	defer cs.channelsMux.RUnlock()
	return cs.Channels[name]
}

func (cs *ClientState) GetChannels() map[string]*parser.Channel {
	cs.channelsMux.RLock()
	defer cs.channelsMux.RUnlock()
	channels := make(map[string]*parser.Channel, len(cs.Channels))
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
	cs.Channels = make(map[string]*parser.Channel)
	cs.ServerInfo = NewServerInfo()
}
