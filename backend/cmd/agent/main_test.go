// Checks agent startup rejects invalid health-listener and MCP addresses.

package main

import (
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/config"
)

func TestRunRejectsInvalidListenOrMCPAddress(t *testing.T) {
	configuration := config.Config{AI: config.AI{Environment: "cluster", AgentHealthAddress: "bad address", MCPURL: "://bad", DecisionTimeout: time.Millisecond, CapabilitySecret: "secret"}}
	if err := run(configuration); err == nil {
		t.Fatal("invalid runtime addresses accepted")
	}
	if hostname() == "" {
		t.Fatal("hostname empty")
	}
}
