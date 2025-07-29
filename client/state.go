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
	UserModes       map[rune]string // mode -> descriptionw
	ChannelPrefixes map[rune]rune   // prefix -> mode (e.g., '@' -> 'o')

	// Channel limits per prefix type (e.g., '#' -> 20, '&' -> 10)
	ChannelLimits map[rune]int

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
	si := &ServerInfo{
		MaxLineLength:   512,
		MaxTopicLength:  390,
		MaxChannels:     10,
		MaxNickLength:   9,
		MaxAwayLength:   160,
		ChannelModes:    make(map[rune]string),
		UserModes:       make(map[rune]string),
		ChannelPrefixes: make(map[rune]rune),
		ChannelLimits:   make(map[rune]int),
		ISupport:        make(map[string]string),
		Capabilities:    make(map[string]string),
		CaseMapping:     "ascii",
	}

	// Set default PREFIX mapping: (ov)@+
	si.ChannelPrefixes['@'] = 'o' // @ prefix for op mode
	si.ChannelPrefixes['+'] = 'v' // + prefix for voice mode

	return si
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
				si.MaxChannels = val
			}
		}
	case "LINELEN":
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
	case "MAXCHANNELS":
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
	case "CHANLIMIT":
		si.parseChannelLimits(value)
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

	// Clear existing prefixes and replace with server-provided ones
	si.ChannelPrefixes = make(map[rune]rune)

	// Map each prefix to its corresponding mode
	for i := 0; i < len(modes); i++ {
		si.ChannelPrefixes[rune(prefixes[i])] = rune(modes[i])
	}
}

func (si *ServerInfo) parseChannelLimits(value string) {
	// Parse CHANLIMIT value (e.g., "#:20,&:10")
	// Format: prefix:number[,prefix:number[,...]]
	// Each prefix corresponds to a channel type with its limit

	if len(value) == 0 {
		return
	}

	// Clear existing limits
	si.ChannelLimits = make(map[rune]int)

	// Split by comma to get individual limits
	limits := strings.Split(value, ",")
	for _, limit := range limits {
		// Split by colon to separate prefix from number
		parts := strings.Split(limit, ":")
		if len(parts) != 2 {
			continue // Invalid format, skip this entry
		}

		prefixes := parts[0]
		numberStr := parts[1]

		// Parse the number
		number := parseInt(numberStr)
		if number <= 0 {
			continue // Invalid number, skip this entry
		}

		// Apply the limit to all prefixes in the group
		for _, prefix := range prefixes {
			si.ChannelLimits[prefix] = number
		}
	}
}

// parseInt parses a string to int, returns 0 if invalid
func parseInt(s string) int {
	if val, err := strconv.Atoi(s); err == nil {
		return val
	}
	return 0
}

// State represents the complete state of an IRC client
type State struct {
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

func NewClientState() *State {
	return &State{
		ConnectionState: parser.StateDisconnected,
		ServerInfo:      NewServerInfo(),
		Channels:        make(map[string]*parser.Channel),
		UserModes:       make([]parser.UserMode, 0),
	}
}

func (cs *State) SetConnectionState(state parser.ConnectionState) {
	cs.connMux.Lock()
	defer cs.connMux.Unlock()
	cs.ConnectionState = state
}

func (cs *State) GetConnectionState() parser.ConnectionState {
	cs.connMux.RLock()
	defer cs.connMux.RUnlock()
	return cs.ConnectionState
}

func (cs *State) IsConnected() bool {
	state := cs.GetConnectionState()
	return state == parser.StateConnected || state == parser.StateRegistered
}

func (cs *State) IsRegistered() bool {
	return cs.GetConnectionState() == parser.StateRegistered
}

func (cs *State) SetCurrentNick(nick string) {
	cs.userMux.Lock()
	defer cs.userMux.Unlock()
	cs.CurrentNick = nick
}

func (cs *State) GetCurrentNick() string {
	cs.userMux.RLock()
	defer cs.userMux.RUnlock()
	return cs.CurrentNick
}

func (cs *State) SetPreferredNick(nick string) {
	cs.userMux.Lock()
	defer cs.userMux.Unlock()
	cs.PreferredNick = nick
}

func (cs *State) GetPreferredNick() string {
	cs.userMux.RLock()
	defer cs.userMux.RUnlock()
	return cs.PreferredNick
}

func (cs *State) SetUserInfo(username, realname string) {
	cs.userMux.Lock()
	defer cs.userMux.Unlock()
	cs.Username = username
	cs.Realname = realname
}

func (cs *State) GetUserInfo() (string, string) {
	cs.userMux.RLock()
	defer cs.userMux.RUnlock()
	return cs.Username, cs.Realname
}

func (cs *State) AddUserMode(mode rune, parameter string) {
	cs.userMux.Lock()
	defer cs.userMux.Unlock()
	for i, m := range cs.UserModes {
		if m.Mode == mode {
			cs.UserModes[i] = parser.UserMode{Mode: mode, Parameter: parameter}
			return
		}
	}
	cs.UserModes = append(cs.UserModes, parser.UserMode{Mode: mode, Parameter: parameter})
}

func (cs *State) RemoveUserMode(mode rune) {
	cs.userMux.Lock()
	defer cs.userMux.Unlock()
	for i, m := range cs.UserModes {
		if m.Mode == mode {
			cs.UserModes = append(cs.UserModes[:i], cs.UserModes[i+1:]...)
			return
		}
	}
}

func (cs *State) HasUserMode(mode rune) bool {
	cs.userMux.RLock()
	defer cs.userMux.RUnlock()
	for _, m := range cs.UserModes {
		if m.Mode == mode {
			return true
		}
	}
	return false
}

func (cs *State) GetUserModes() []parser.UserMode {
	cs.userMux.RLock()
	defer cs.userMux.RUnlock()
	modes := make([]parser.UserMode, len(cs.UserModes))
	copy(modes, cs.UserModes)
	return modes
}

func (cs *State) AddChannel(channel *parser.Channel) {
	cs.channelsMux.Lock()
	defer cs.channelsMux.Unlock()
	cs.Channels[channel.Name] = channel
}

func (cs *State) RemoveChannel(name string) {
	cs.channelsMux.Lock()
	defer cs.channelsMux.Unlock()
	delete(cs.Channels, name)
}

func (cs *State) GetChannel(name string) *parser.Channel {
	cs.channelsMux.RLock()
	defer cs.channelsMux.RUnlock()
	return cs.Channels[name]
}

func (cs *State) GetChannels() map[string]*parser.Channel {
	cs.channelsMux.RLock()
	defer cs.channelsMux.RUnlock()
	channels := make(map[string]*parser.Channel, len(cs.Channels))
	for name, channel := range cs.Channels {
		channels[name] = channel
	}
	return channels
}

func (cs *State) GetChannelNames() []string {
	cs.channelsMux.RLock()
	defer cs.channelsMux.RUnlock()
	names := make([]string, 0, len(cs.Channels))
	for name := range cs.Channels {
		names = append(names, name)
	}
	return names
}

func (cs *State) IsInChannel(name string) bool {
	cs.channelsMux.RLock()
	defer cs.channelsMux.RUnlock()
	_, exists := cs.Channels[name]
	return exists
}

func (cs *State) Reset() {
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
