package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlainMechanism(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		testFunc func(t *testing.T, username, password string)
	}{
		{
			name:     "secure credential handling",
			username: "testuser",
			password: "testpass",
			testFunc: testSecureCredentialHandling,
		},
		{
			name:     "next returns error",
			username: "user",
			password: "pass",
			testFunc: testNextReturnsError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.testFunc(t, tt.username, tt.password)
		})
	}
}

func testSecureCredentialHandling(t *testing.T, username, password string) {
	mechanism := NewPlainMechanism(username, password)

	assert.Equal(t, "PLAIN", mechanism.Name())

	encoded, err := mechanism.Start()
	assert.NoError(t, err)
	assert.NotEmpty(t, encoded)

	assert.True(t, mechanism.IsComplete(), "Mechanism should be complete after Start()")

	mechanism.Reset()

	assert.False(t, mechanism.IsComplete(), "Mechanism should not be complete after Reset()")
	assert.Nil(t, mechanism.username, "Username should be nil after Reset()")
	assert.Nil(t, mechanism.password, "Password should be nil after Reset()")
}

func testNextReturnsError(t *testing.T, username, password string) {
	mechanism := NewPlainMechanism(username, password)

	_, err := mechanism.Next("challenge")
	assert.Error(t, err, "Next() should return error for PLAIN mechanism")
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
			assert.Error(t, err, "Start() should return error for empty credentials")
		})
	}
}
