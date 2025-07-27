package parser

import "fmt"

type IRCError struct {
	Operation string
	Message   string
	Err       error
}

func (e *IRCError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s (%v)", e.Operation, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Operation, e.Message)
}

func (e *IRCError) Unwrap() error {
	return e.Err
}

func NewConnectionError(operation, message string, err error) *IRCError {
	return &IRCError{
		Operation: operation,
		Message:   message,
		Err:       err,
	}
}

func NewMessageError(operation, message string, err error) *IRCError {
	return &IRCError{
		Operation: operation,
		Message:   message,
		Err:       err,
	}
}

func NewRegistrationError(operation, message string) *IRCError {
	return &IRCError{
		Operation: operation,
		Message:   message,
	}
}

func NewSASLError(operation, message string, err error) *IRCError {
	return &IRCError{
		Operation: operation,
		Message:   message,
		Err:       err,
	}
}
