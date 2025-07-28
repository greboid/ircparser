package parser

import (
	"sync"
	"time"
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

// GetPrefixString converts the user's modes to prefix characters using the provided channel prefix mapping
// prefixToModeMap should map prefix characters to their mode symbols (e.g., '@' -> 'o', '+' -> 'v')
// This function reverses the mapping internally to convert modes back to prefixes
func (u *User) GetPrefixString(prefixToModeMap map[rune]rune) string {
	u.mux.RLock()
	defer u.mux.RUnlock()

	// Create reverse mapping from mode to prefix
	modeToPrefix := make(map[rune]rune)
	for prefix, mode := range prefixToModeMap {
		modeToPrefix[mode] = prefix
	}

	var prefixes []rune
	for _, mode := range u.Modes {
		if prefix, exists := modeToPrefix[mode.Mode]; exists {
			prefixes = append(prefixes, prefix)
		}
	}

	return string(prefixes)
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
