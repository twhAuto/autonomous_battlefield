// Checks MCP startup reports a failed database connection.

package main

import (
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/config"
)

func TestRunRejectsInvalidDatabaseConnection(t *testing.T) {
	configuration := config.Config{Database: config.Database{URL: "://bad", ConnectTimeout: time.Millisecond, MaxConnections: 1}, AI: config.AI{CapabilitySecret: "secret", MCPAddress: ":8084"}}
	if err := run(configuration); err == nil {
		t.Fatal("invalid database accepted")
	}
}
