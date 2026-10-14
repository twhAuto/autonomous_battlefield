// Checks agent startup rejects invalid health-listener and MCP addresses.

package main

import (
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/config"
)

func TestRunRejectsInvalidListenOrMCPAddress(t *testing.T) {
	configuration := config.AgentConfig{Models: config.Models{Environment: "cluster", DecisionTimeout: time.Millisecond}, Agent: config.Agent{Concurrency: 1, HealthAddress: "bad address", MCPURL: "://bad"}}
	if err := run(configuration); err == nil {
		t.Fatal("invalid runtime addresses accepted")
	}
	if hostname() == "" {
		t.Fatal("hostname empty")
	}
}
