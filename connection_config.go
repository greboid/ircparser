package parser

import (
	"time"
)

type ConnectionConfig struct {
	Host           string
	Port           int
	TLS            bool
	Password       string
	Nick           string
	Username       string
	Realname       string
	SASLUser       string
	SASLPass       string
	ConnTimeout    time.Duration
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	PingTimeout    time.Duration
	ReconnectDelay time.Duration
}

func NewConnectionConfig(host string, port int, tls bool) *ConnectionConfig {
	return &ConnectionConfig{
		Host:           host,
		Port:           port,
		TLS:            tls,
		ConnTimeout:    DefaultConnTimeout,
		ReadTimeout:    DefaultReadTimeout,
		WriteTimeout:   DefaultWriteTimeout,
		PingTimeout:    DefaultPingTimeout,
		ReconnectDelay: DefaultReconnectDelay,
	}
}
