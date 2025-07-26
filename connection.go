package parser

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	DefaultConnTimeout    = 30 * time.Second
	DefaultReadTimeout    = 300 * time.Second // 5 minutes
	DefaultWriteTimeout   = 30 * time.Second
	DefaultReconnectDelay = 5 * time.Second
	WriteFlushInterval    = 100 * time.Millisecond
	ConnectionWorkerCount = 2
)

type Connection struct {
	config   *ConnectionConfig
	conn     net.Conn
	reader   *bufio.Reader
	writer   *bufio.Writer
	state    ConnectionState
	stateMux sync.RWMutex
	writeMux sync.Mutex
	eventBus *EventBus
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup

	currentNick string
	nickMux     sync.RWMutex

	isupport    map[string]string
	isupportMux sync.RWMutex
}

func NewConnection(ctx context.Context, config *ConnectionConfig, eventBus *EventBus) *Connection {
	slog.Debug("Creating new connection", "host", config.Host, "port", config.Port, "tls", config.TLS)
	ctx, cancel := context.WithCancel(ctx)

	return &Connection{
		config:   config,
		state:    StateDisconnected,
		eventBus: eventBus,
		ctx:      ctx,
		cancel:   cancel,
		isupport: make(map[string]string),
	}
}

func (c *Connection) Connect() error {
	slog.Info("Initiating IRC connection",
		"host", c.config.Host,
		"port", c.config.Port,
		"tls", c.config.TLS,
		"timeout", c.config.ConnTimeout)

	c.setState(StateConnecting)

	address := fmt.Sprintf("%s:%d", c.config.Host, c.config.Port)

	var conn net.Conn
	var err error

	if c.config.TLS {
		slog.Debug("Establishing TLS connection", "address", address)
		tlsConfig := &tls.Config{
			ServerName: c.config.Host,
		}
		conn, err = tls.DialWithDialer(&net.Dialer{
			Timeout: c.config.ConnTimeout,
		}, "tcp", address, tlsConfig)
	} else {
		slog.Debug("Establishing plain TCP connection", "address", address)
		conn, err = net.DialTimeout("tcp", address, c.config.ConnTimeout)
	}

	if err != nil {
		c.setState(StateDisconnected)
		slog.Error("Connection failed", "address", address, "error", err)
		return fmt.Errorf("failed to connect to %s: %w", address, err)
	}

	c.conn = conn
	c.reader = bufio.NewReader(conn)
	c.writer = bufio.NewWriter(conn)
	c.setState(StateConnected)

	slog.Info("IRC connection established", "address", address, "tls", c.config.TLS)

	c.wg.Add(ConnectionWorkerCount)
	go c.readLoop()
	go c.writeLoop()

	c.eventBus.Emit(&Event{
		Type: EventConnected,
		Data: &ConnectedData{
			ServerName: c.config.Host,
		},
	})

	return nil
}

func (c *Connection) Disconnect() {
	slog.Info("Initiating IRC disconnection", "host", c.config.Host)

	c.setState(StateDisconnecting)

	c.cancel()

	if c.conn != nil {
		err := c.conn.Close()
		if err != nil {
			slog.Debug("Error closing connection", "error", err)
		}
	}

	c.wg.Wait()

	c.setState(StateDisconnected)

	slog.Info("IRC connection closed", "host", c.config.Host)
	c.eventBus.Emit(&Event{
		Type: EventDisconnected,
		Data: &DisconnectedData{
			Reason: "manual disconnect",
		},
	})
}

func (c *Connection) readLoop() {
	slog.Debug("Starting read loop", "host", c.config.Host)
	defer c.wg.Done()
	defer func() {
		if c.conn != nil {
			err := c.conn.Close()
			if err != nil {
				slog.Debug("Error closing connection", "error", err)
			}
		}
		slog.Debug("Read loop ended", "host", c.config.Host)
	}()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		if c.config.ReadTimeout > 0 && c.conn != nil {
			c.conn.SetReadDeadline(time.Now().Add(c.config.ReadTimeout))
		}

		if c.reader == nil {
			return
		}

		line, err := c.reader.ReadString('\n')
		if err != nil {
			if c.getState() != StateDisconnecting {
				slog.Error("Read error in connection", "host", c.config.Host, "error", err)
				c.eventBus.Emit(&Event{
					Type: EventDisconnected,
					Data: &DisconnectedData{
						Reason: "read error",
						Error:  err,
					},
				})
			}
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		msg, err := ParseMessage(line)

		c.eventBus.Emit(&Event{
			Type: EventRawIncoming,
			Data: &RawIncomingData{
				Line:      line,
				Timestamp: time.Now(),
			},
		})

		if err != nil {
			slog.Error("Failed to parse message", "host", c.config.Host, "line", line, "error", err)
			c.eventBus.Emit(&Event{
				Type: EventError,
				Data: &ErrorData{
					Message: fmt.Sprintf("failed to parse message: %s", err),
				},
			})
			continue
		}

		c.handleMessage(msg)
	}
}

func (c *Connection) writeLoop() {
	slog.Debug("Starting write loop", "host", c.config.Host)
	defer c.wg.Done()
	defer func() {
		slog.Debug("Write loop ended", "host", c.config.Host)
	}()

	ticker := time.NewTicker(WriteFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.writeMux.Lock()
			if c.writer != nil {
				err := c.writer.Flush()
				if err != nil {
					slog.Debug("Error flushing connection", "error", err)
				}
			}
			c.writeMux.Unlock()
		}
	}
}

func (c *Connection) handleMessage(msg *Message) {
	switch msg.Command {
	case NumericWelcome:
		oldNick := c.GetCurrentNick()
		c.setState(StateRegistered)
		if len(msg.Params) > 0 {
			c.setCurrentNick(msg.Params[0])
		}
		slog.Info("IRC connection received 001 welcome",
			"server", c.config.Host,
			"nick", c.GetCurrentNick(),
			"old_nick", oldNick)

	case NumericISupport:
		c.parseISUPPORT(msg)
		slog.Debug("Parsed ISUPPORT", "feature_count", len(c.isupport))

	case "NICK":
		oldNick := extractNick(msg.Source)
		newNick := msg.GetParam(0)
		if oldNick == c.GetCurrentNick() {
			c.setCurrentNick(newNick)
			slog.Debug("Nick changed", "old_nick", oldNick, "new_nick", newNick)
		}
	}

	events := CreateEventFromMessage(msg)
	for _, event := range events {
		if event != nil {
			c.eventBus.Emit(event)
		}
	}
}

func (c *Connection) parseISUPPORT(msg *Message) {
	slog.Debug("Parsing ISUPPORT", "host", c.config.Host, "params", msg.Params)
	c.isupportMux.Lock()
	defer c.isupportMux.Unlock()

	for i := 1; i < len(msg.Params)-1; i++ {
		param := msg.Params[i]
		if strings.Contains(param, "=") {
			parts := strings.SplitN(param, "=", 2)
			c.isupport[parts[0]] = parts[1]
		} else {
			c.isupport[param] = ""
		}
	}
	slog.Debug("ISUPPORT parsed", "host", c.config.Host, "feature_count", len(c.isupport))
}

func (c *Connection) SendRaw(line string) error {
	c.writeMux.Lock()
	defer c.writeMux.Unlock()
	if c.getState() == StateDisconnected {
		slog.Warn("Attempt to send while disconnected", "host", c.config.Host, "line", line)
		return fmt.Errorf("not connected")
	}
	if c.writer == nil {
		slog.Error("Writer not available", "host", c.config.Host)
		return fmt.Errorf("writer not available")
	}

	slog.Log(context.Background(), LogTrace, "--> "+line)
	c.eventBus.Emit(&Event{
		Type: EventRawOutgoing,
		Data: &RawOutgoingData{
			Line:      line,
			Timestamp: time.Now(),
		},
	})

	if c.config.WriteTimeout > 0 && c.conn != nil {
		c.conn.SetWriteDeadline(time.Now().Add(c.config.WriteTimeout))
	}

	_, err := c.writer.WriteString(line + "\r\n")
	if err != nil {
		slog.Error("Failed to write line", "host", c.config.Host, "line", line, "error", err)

		// Check if this is a critical error that indicates connection failure
		if c.isCriticalWriteError(err) {
			slog.Warn("Critical write error detected, triggering disconnect", "host", c.config.Host, "error", err)
			c.handleCriticalWriteError(err)
		}
	}
	return err
}

func (c *Connection) Send(command string, params ...string) error {
	slog.Debug("Sending", "host", c.config.Host, "command", command, "params", params)
	msg := &Message{
		Command: command,
		Params:  params,
	}
	err := c.SendRaw(msg.String())
	if err != nil {
		slog.Error("Failed to send command", "host", c.config.Host, "command", command, "params", params, "error", err)
	}
	return err
}

func (c *Connection) getState() ConnectionState {
	c.stateMux.RLock()
	defer c.stateMux.RUnlock()
	return c.state
}

func (c *Connection) setState(state ConnectionState) {
	c.stateMux.Lock()
	defer c.stateMux.Unlock()
	c.state = state
}

func (c *Connection) GetState() ConnectionState {
	return c.getState()
}

func (c *Connection) IsConnected() bool {
	state := c.getState()
	return state == StateConnected || state == StateRegistering || state == StateRegistered
}

func (c *Connection) IsRegistered() bool {
	return c.getState() == StateRegistered
}

func (c *Connection) setCurrentNick(nick string) {
	c.nickMux.Lock()
	defer c.nickMux.Unlock()
	c.currentNick = nick
}

func (c *Connection) GetCurrentNick() string {
	c.nickMux.RLock()
	defer c.nickMux.RUnlock()
	return c.currentNick
}

func (c *Connection) GetISupport() map[string]string {
	c.isupportMux.RLock()
	defer c.isupportMux.RUnlock()

	isupport := make(map[string]string)
	maps.Copy(isupport, c.isupport)
	return isupport
}

func (c *Connection) GetISupportValue(key string) string {
	c.isupportMux.RLock()
	defer c.isupportMux.RUnlock()

	return c.isupport[key]
}

func (c *Connection) GetServerName() string {
	return c.config.Host
}

func (c *Connection) GetConfig() *ConnectionConfig {
	return c.config
}

func (c *Connection) Wait() {
	c.wg.Wait()
}

// isCriticalWriteError determines if a write error indicates connection failure
func (c *Connection) isCriticalWriteError(err error) bool {
	if err == nil {
		return false
	}

	// Check for network-level errors that indicate connection failure
	var netErr net.Error
	if errors.As(err, &netErr) {
		// Timeout errors during writes often indicate connection issues
		if netErr.Timeout() {
			return true
		}
	}

	// Check for system-level errors
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		// Check for syscall errors that indicate connection failure
		if errors.Is(opErr.Err, syscall.EPIPE) ||
			errors.Is(opErr.Err, syscall.ECONNRESET) ||
			errors.Is(opErr.Err, syscall.ECONNABORTED) ||
			errors.Is(opErr.Err, syscall.ENOTCONN) {
			return true
		}
	}

	return false
}

// handleCriticalWriteError handles critical write errors by triggering disconnection
func (c *Connection) handleCriticalWriteError(err error) {
	// Only handle if we're not already disconnecting
	if c.getState() == StateDisconnecting || c.getState() == StateDisconnected {
		return
	}

	// Update connection state to disconnected
	c.setState(StateDisconnected)

	// Emit disconnect event
	c.eventBus.Emit(&Event{
		Type: EventDisconnected,
		Data: &DisconnectedData{
			Reason: "write error",
			Error:  err,
		},
	})
}
