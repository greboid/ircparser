package parser

import (
	"errors"
	"fmt"
)

// ErrorType represents the category of error
type ErrorType string

const (
	ErrorTypeConnection   ErrorType = "connection"
	ErrorTypeRegistration ErrorType = "registration"
	ErrorTypeMessage      ErrorType = "message"
	ErrorTypeSASL         ErrorType = "sasl"
	ErrorTypeProtocol     ErrorType = "protocol"
)

// IRCError represents a domain-specific IRC error
type IRCError struct {
	Type    ErrorType
	Op      string // operation that failed
	Message string
	Err     error // underlying error
}

func (e *IRCError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("IRC %s error in %s: %s: %v", e.Type, e.Op, e.Message, e.Err)
	}
	return fmt.Sprintf("IRC %s error in %s: %s", e.Type, e.Op, e.Message)
}

func (e *IRCError) Unwrap() error {
	return e.Err
}

// Is allows error matching
func (e *IRCError) Is(target error) bool {
	if t, ok := target.(*IRCError); ok {
		return e.Type == t.Type
	}
	return false
}

// Convenience constructors for different error types

// NewConnectionError creates a connection-related error
func NewConnectionError(op, message string, err error) *IRCError {
	return &IRCError{
		Type:    ErrorTypeConnection,
		Op:      op,
		Message: message,
		Err:     err,
	}
}

// NewRegistrationError creates a registration-related error
func NewRegistrationError(op, message string) *IRCError {
	return &IRCError{
		Type:    ErrorTypeRegistration,
		Op:      op,
		Message: message,
	}
}

// NewMessageError creates a message parsing error
func NewMessageError(op, message string, err error) *IRCError {
	return &IRCError{
		Type:    ErrorTypeMessage,
		Op:      op,
		Message: message,
		Err:     err,
	}
}

// NewSASLError creates a SASL authentication error
func NewSASLError(op, message string, err error) *IRCError {
	return &IRCError{
		Type:    ErrorTypeSASL,
		Op:      op,
		Message: message,
		Err:     err,
	}
}

// NewProtocolError creates a protocol violation error
func NewProtocolError(op, message string, err error) *IRCError {
	return &IRCError{
		Type:    ErrorTypeProtocol,
		Op:      op,
		Message: message,
		Err:     err,
	}
}

// Error checking functions

// IsConnectionError checks if error is a connection error
func IsConnectionError(err error) bool {
	var ircErr *IRCError
	return err != nil && errors.As(err, &ircErr) && ircErr.Type == ErrorTypeConnection
}

// IsRegistrationError checks if error is a registration error
func IsRegistrationError(err error) bool {
	var ircErr *IRCError
	return err != nil && errors.As(err, &ircErr) && ircErr.Type == ErrorTypeRegistration
}

// IsMessageError checks if error is a message parsing error
func IsMessageError(err error) bool {
	var ircErr *IRCError
	return err != nil && errors.As(err, &ircErr) && ircErr.Type == ErrorTypeMessage
}

// IsSASLError checks if error is a SASL error
func IsSASLError(err error) bool {
	var ircErr *IRCError
	return err != nil && errors.As(err, &ircErr) && ircErr.Type == ErrorTypeSASL
}

// IsProtocolError checks if error is a protocol error
func IsProtocolError(err error) bool {
	var ircErr *IRCError
	return err != nil && errors.As(err, &ircErr) && ircErr.Type == ErrorTypeProtocol
}
