package parser

type ConnectionState int

const (
	StateDisconnected ConnectionState = iota
	StateConnecting
	StateConnected
	StateRegistering
	StateRegistered
	StateDisconnecting
)

func (s ConnectionState) String() string {
	switch s {
	case StateDisconnected:
		return "disconnected"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateRegistering:
		return "registering"
	case StateRegistered:
		return "registered"
	case StateDisconnecting:
		return "disconnecting"
	default:
		return "unknown"
	}
}
