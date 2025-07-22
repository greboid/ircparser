package parser

import (
	"encoding/base64"
	"fmt"
)

type SASLMechanism interface {
	Name() string
	Start() (string, error)
	Next(challenge string) (string, error)
	IsComplete() bool
	Reset()
}

type PlainMechanism struct {
	username string
	password string
	complete bool
}

func (pm *PlainMechanism) Name() string {
	return "PLAIN"
}

func (pm *PlainMechanism) Start() (string, error) {
	if pm.username == "" || pm.password == "" {
		return "", fmt.Errorf("username and password required for PLAIN")
	}

	authString := fmt.Sprintf("%s\x00%s\x00%s", pm.username, pm.username, pm.password)
	encoded := base64.StdEncoding.EncodeToString([]byte(authString))
	pm.complete = true
	return encoded, nil
}

func (pm *PlainMechanism) Next(string) (string, error) {
	return "", fmt.Errorf("PLAIN mechanism does not support challenges")
}

func (pm *PlainMechanism) IsComplete() bool {
	return pm.complete
}

func (pm *PlainMechanism) Reset() {
	pm.complete = false
}
