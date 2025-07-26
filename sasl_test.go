package parser

import (
	"testing"
)

func TestNewPlainMechanism_SecureCredentialHandling(t *testing.T) {
	username := "testuser"
	password := "testpass"

	// Create mechanism using secure constructor
	mechanism := NewPlainMechanism(username, password)

	// Verify mechanism is properly initialized
	if mechanism.Name() != "PLAIN" {
		t.Errorf("Expected PLAIN, got %s", mechanism.Name())
	}

	// Test Start() method
	encoded, err := mechanism.Start()
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	if encoded == "" {
		t.Error("Start() returned empty encoded string")
	}

	// Verify mechanism is marked complete
	if !mechanism.IsComplete() {
		t.Error("Mechanism should be complete after Start()")
	}

	// Test Reset() method - this should zero out credentials
	mechanism.Reset()

	// Verify reset worked
	if mechanism.IsComplete() {
		t.Error("Mechanism should not be complete after Reset()")
	}

	// Verify credentials are zeroed (should be nil after reset)
	if mechanism.username != nil || mechanism.password != nil {
		t.Error("Credentials should be nil after Reset()")
	}
}

func TestPlainMechanism_Next_ReturnsError(t *testing.T) {
	mechanism := NewPlainMechanism("user", "pass")

	_, err := mechanism.Next("challenge")
	if err == nil {
		t.Error("Next() should return error for PLAIN mechanism")
	}
}

func TestPlainMechanism_Start_EmptyCredentials(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
	}{
		{"empty username", "", "password"},
		{"empty password", "username", ""},
		{"both empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mechanism := NewPlainMechanism(tt.username, tt.password)
			_, err := mechanism.Start()
			if err == nil {
				t.Error("Start() should return error for empty credentials")
			}
		})
	}
}
