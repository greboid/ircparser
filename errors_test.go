package parser

import (
	"errors"
	"testing"
)

func TestIRCError_Error(t *testing.T) {
	tests := []struct {
		name     string
		ircError *IRCError
		expected string
	}{
		{
			name: "error with underlying error",
			ircError: &IRCError{
				Type:    ErrorTypeConnection,
				Op:      "Connect",
				Message: "connection failed",
				Err:     errors.New("network unreachable"),
			},
			expected: "IRC connection error in Connect: connection failed: network unreachable",
		},
		{
			name: "error without underlying error",
			ircError: &IRCError{
				Type:    ErrorTypeRegistration,
				Op:      "Join",
				Message: "not registered",
			},
			expected: "IRC registration error in Join: not registered",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.ircError.Error()
			if result != tt.expected {
				t.Errorf("Error() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIRCError_Unwrap(t *testing.T) {
	underlyingErr := errors.New("underlying error")
	ircErr := &IRCError{
		Type:    ErrorTypeMessage,
		Op:      "ParseMessage",
		Message: "parse failed",
		Err:     underlyingErr,
	}

	result := ircErr.Unwrap()
	if result != underlyingErr {
		t.Errorf("Unwrap() = %v, want %v", result, underlyingErr)
	}
}

func TestIRCError_Is(t *testing.T) {
	connErr := &IRCError{Type: ErrorTypeConnection}
	regErr := &IRCError{Type: ErrorTypeRegistration}

	// Test same type
	if !connErr.Is(&IRCError{Type: ErrorTypeConnection}) {
		t.Error("Is() should return true for same error type")
	}

	// Test different type
	if connErr.Is(regErr) {
		t.Error("Is() should return false for different error type")
	}

	// Test non-IRCError type
	if connErr.Is(errors.New("regular error")) {
		t.Error("Is() should return false for non-IRCError type")
	}
}

func TestErrorConstructors(t *testing.T) {
	underlyingErr := errors.New("underlying")

	// Test NewConnectionError
	connErr := NewConnectionError("Connect", "failed to connect", underlyingErr)
	if connErr.Type != ErrorTypeConnection {
		t.Error("NewConnectionError should create connection error")
	}
	if connErr.Op != "Connect" {
		t.Error("NewConnectionError should set operation")
	}
	if connErr.Message != "failed to connect" {
		t.Error("NewConnectionError should set message")
	}
	if connErr.Err != underlyingErr {
		t.Error("NewConnectionError should set underlying error")
	}

	// Test NewRegistrationError
	regErr := NewRegistrationError("Join", "not registered")
	if regErr.Type != ErrorTypeRegistration {
		t.Error("NewRegistrationError should create registration error")
	}
	if regErr.Err != nil {
		t.Error("NewRegistrationError should not set underlying error")
	}

	// Test NewMessageError
	msgErr := NewMessageError("ParseMessage", "invalid format", underlyingErr)
	if msgErr.Type != ErrorTypeMessage {
		t.Error("NewMessageError should create message error")
	}

	// Test NewSASLError
	saslErr := NewSASLError("StartAuthentication", "auth failed", underlyingErr)
	if saslErr.Type != ErrorTypeSASL {
		t.Error("NewSASLError should create SASL error")
	}

	// Test NewProtocolError
	protoErr := NewProtocolError("SendCommand", "protocol violation", underlyingErr)
	if protoErr.Type != ErrorTypeProtocol {
		t.Error("NewProtocolError should create protocol error")
	}
}

func TestErrorCheckers(t *testing.T) {
	connErr := NewConnectionError("Connect", "failed", nil)
	regErr := NewRegistrationError("Join", "not registered")
	msgErr := NewMessageError("Parse", "invalid", nil)
	saslErr := NewSASLError("Auth", "failed", nil)
	protoErr := NewProtocolError("Command", "invalid", nil)
	regularErr := errors.New("regular error")

	// Test IsConnectionError
	if !IsConnectionError(connErr) {
		t.Error("IsConnectionError should return true for connection error")
	}
	if IsConnectionError(regErr) {
		t.Error("IsConnectionError should return false for non-connection error")
	}
	if IsConnectionError(regularErr) {
		t.Error("IsConnectionError should return false for regular error")
	}
	if IsConnectionError(nil) {
		t.Error("IsConnectionError should return false for nil error")
	}

	// Test IsRegistrationError
	if !IsRegistrationError(regErr) {
		t.Error("IsRegistrationError should return true for registration error")
	}
	if IsRegistrationError(connErr) {
		t.Error("IsRegistrationError should return false for non-registration error")
	}

	// Test IsMessageError
	if !IsMessageError(msgErr) {
		t.Error("IsMessageError should return true for message error")
	}
	if IsMessageError(connErr) {
		t.Error("IsMessageError should return false for non-message error")
	}

	// Test IsSASLError
	if !IsSASLError(saslErr) {
		t.Error("IsSASLError should return true for SASL error")
	}
	if IsSASLError(connErr) {
		t.Error("IsSASLError should return false for non-SASL error")
	}

	// Test IsProtocolError
	if !IsProtocolError(protoErr) {
		t.Error("IsProtocolError should return true for protocol error")
	}
	if IsProtocolError(connErr) {
		t.Error("IsProtocolError should return false for non-protocol error")
	}
}
