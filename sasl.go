package parser

import (
	"encoding/base64"
)

type SASLMechanism interface {
	Name() string
	Start() (string, error)
	Next(challenge string) (string, error)
	IsComplete() bool
	Reset()
}

type PlainMechanism struct {
	username []byte
	password []byte
	complete bool
}

func NewPlainMechanism(username, password string) *PlainMechanism {
	return &PlainMechanism{
		username: []byte(username),
		password: []byte(password),
	}
}

func (pm *PlainMechanism) Name() string {
	return "PLAIN"
}

func (pm *PlainMechanism) Start() (string, error) {
	if len(pm.username) == 0 || len(pm.password) == 0 {
		return "", NewSASLError("Start", "username and password required for PLAIN mechanism", nil)
	}

	// Format: username\x00username\x00password
	authBytes := make([]byte, len(pm.username)*2+len(pm.password)+2)
	offset := 0
	copy(authBytes[offset:], pm.username)
	offset += len(pm.username)
	authBytes[offset] = 0
	offset++
	copy(authBytes[offset:], pm.username)
	offset += len(pm.username)
	authBytes[offset] = 0
	offset++
	copy(authBytes[offset:], pm.password)

	encoded := base64.StdEncoding.EncodeToString(authBytes)

	for i := range authBytes {
		authBytes[i] = 0
	}
	for i := range pm.username {
		pm.username[i] = 0
	}
	for i := range pm.password {
		pm.password[i] = 0
	}
	pm.username = nil
	pm.password = nil
	pm.complete = true
	return encoded, nil
}

func (pm *PlainMechanism) Next(string) (string, error) {
	return "", NewSASLError("Next", "PLAIN mechanism does not support challenge-response", nil)
}

func (pm *PlainMechanism) IsComplete() bool {
	return pm.complete
}

func (pm *PlainMechanism) Reset() {
	pm.complete = false
}
