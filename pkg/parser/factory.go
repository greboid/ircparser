package parser

import (
	"context"
)

// SetupDefaultHandlers sets up the default IRC handlers for a parser
func SetupDefaultHandlers(ctx context.Context, connection *Connection, eventBus *EventBus, config *ConnectionConfig, capabilities map[string]string) {
	if config.SASLUser() != "" && config.SASLPass() != "" {
		NewSASLHandler(ctx, connection.Send, eventBus.Subscribe, eventBus.Emit, config.SASLUser(), config.SASLPass())
	}
	NewCapabilitiesHandler(ctx, connection.Send, eventBus.Subscribe, eventBus.Emit, eventBus.CountListeners, capabilities, "sasl", "echo-message")
	NewRegistrationHandler(ctx, connection.config, connection.Send, eventBus.Subscribe, eventBus.UnsubscribeByID, eventBus.Emit)
	NewPingHandler(ctx, connection.Send, eventBus.Subscribe, eventBus.Emit)
}
