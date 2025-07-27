package parser

import (
	"time"
)

const (
	DefaultConnTimeout    = 30 * time.Second
	DefaultReadTimeout    = 300 * time.Second // 5 minutes
	DefaultWriteTimeout   = 30 * time.Second
	DefaultPingTimeout    = 60 * time.Second
	DefaultReconnectDelay = 5 * time.Second
	WriteFlushInterval    = 100 * time.Millisecond
	ConnectionWorkerCount = 2
)

type ConnectionConfig struct {
	host           string
	port           int
	tls            bool
	password       string
	nick           string
	username       string
	realname       string
	saslUser       string
	saslPass       string
	connTimeout    time.Duration
	readTimeout    time.Duration
	writeTimeout   time.Duration
	pingTimeout    time.Duration
	reconnectDelay time.Duration
}

type Option func(*ConnectionConfig)

func NewConnectionConfig(host string, options ...Option) *ConnectionConfig {
	config := &ConnectionConfig{
		host:           host,
		port:           6697,
		tls:            true,
		connTimeout:    DefaultConnTimeout,
		readTimeout:    DefaultReadTimeout,
		writeTimeout:   DefaultWriteTimeout,
		pingTimeout:    DefaultPingTimeout,
		reconnectDelay: DefaultReconnectDelay,
	}

	for _, option := range options {
		option(config)
	}

	return config
}

func WithPort(port int) Option {
	return func(c *ConnectionConfig) {
		c.port = port
	}
}

func WithTLS(tls bool) Option {
	return func(c *ConnectionConfig) {
		c.tls = tls
	}
}

func WithPassword(password string) Option {
	return func(c *ConnectionConfig) {
		c.password = password
	}
}

func WithNick(nick string) Option {
	return func(c *ConnectionConfig) {
		c.nick = nick
	}
}

func WithUsername(username string) Option {
	return func(c *ConnectionConfig) {
		c.username = username
	}
}

func WithRealname(realname string) Option {
	return func(c *ConnectionConfig) {
		c.realname = realname
	}
}

func WithSASL(username, password string) Option {
	return func(c *ConnectionConfig) {
		c.saslUser = username
		c.saslPass = password
	}
}

func WithConnTimeout(timeout time.Duration) Option {
	return func(c *ConnectionConfig) {
		c.connTimeout = timeout
	}
}

func WithReadTimeout(timeout time.Duration) Option {
	return func(c *ConnectionConfig) {
		c.readTimeout = timeout
	}
}

func WithWriteTimeout(timeout time.Duration) Option {
	return func(c *ConnectionConfig) {
		c.writeTimeout = timeout
	}
}

func WithPingTimeout(timeout time.Duration) Option {
	return func(c *ConnectionConfig) {
		c.pingTimeout = timeout
	}
}

func WithReconnectDelay(delay time.Duration) Option {
	return func(c *ConnectionConfig) {
		c.reconnectDelay = delay
	}
}

func (c *ConnectionConfig) Host() string {
	return c.host
}

func (c *ConnectionConfig) Port() int {
	return c.port
}

func (c *ConnectionConfig) TLS() bool {
	return c.tls
}

func (c *ConnectionConfig) Password() string {
	return c.password
}

func (c *ConnectionConfig) Nick() string {
	return c.nick
}

func (c *ConnectionConfig) Username() string {
	return c.username
}

func (c *ConnectionConfig) Realname() string {
	return c.realname
}

func (c *ConnectionConfig) SASLUser() string {
	return c.saslUser
}

func (c *ConnectionConfig) SASLPass() string {
	return c.saslPass
}

func (c *ConnectionConfig) ConnTimeout() time.Duration {
	return c.connTimeout
}

func (c *ConnectionConfig) ReadTimeout() time.Duration {
	return c.readTimeout
}

func (c *ConnectionConfig) WriteTimeout() time.Duration {
	return c.writeTimeout
}

func (c *ConnectionConfig) PingTimeout() time.Duration {
	return c.pingTimeout
}

func (c *ConnectionConfig) ReconnectDelay() time.Duration {
	return c.reconnectDelay
}
