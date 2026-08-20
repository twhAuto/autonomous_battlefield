package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadReadsConfigFile(t *testing.T) {
	path := writeConfigFile(t, `
http:
  address: ":9000"
  readHeaderTimeout: 2s
  readTimeout: 3s
  writeTimeout: 4s
  idleTimeout: 30s
  shutdownTimeout: 5s
`)

	configuration, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned an error: %v", err)
	}

	if configuration.HTTP.Address != ":9000" {
		t.Fatalf("Address = %q, want %q", configuration.HTTP.Address, ":9000")
	}
	if configuration.HTTP.ReadTimeout != 3*time.Second {
		t.Fatalf("ReadTimeout = %v, want %v", configuration.HTTP.ReadTimeout, 3*time.Second)
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	path := writeConfigFile(t, `
http:
  address: ":8080"
  readHeaderTimeout: 5s
  readTimeout: 10s
  writeTimeout: 10s
  idleTimeout: eventually
  shutdownTimeout: 10s
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() returned no error")
	}
	if !strings.Contains(err.Error(), "http.idleTimeout") {
		t.Fatalf("error %q does not identify http.idleTimeout", err)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	path := writeConfigFile(t, `
http:
  address: ":8080"
  readHeaderTimeout: 5s
  readTimeout: 10s
  writeTimeout: 10s
  idleTimeout: 60s
  shutdownTimeout: 10s
  unknownTimeout: 1s
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() returned no error")
	}
}

func TestLoadEngineDoesNotRequireHTTPConfiguration(t *testing.T) {
	path := writeEngineConfigFile(t, `
engine:
  healthAddress: ":9091"
  pollInterval: 2s
  leaseDuration: 45s
  heartbeatInterval: 10s
  shutdownTimeout: 8s
  maxAttempts: 4
`)
	configuration, err := LoadEngine(path)
	if err != nil {
		t.Fatalf("LoadEngine() returned an error: %v", err)
	}
	if configuration.Engine.HealthAddress != ":9091" || configuration.Engine.MaxAttempts != 4 || configuration.Engine.PollInterval != 2*time.Second {
		t.Fatalf("engine config = %#v", configuration.Engine)
	}
}

func TestLoadEngineRejectsHeartbeatAtLeaseDuration(t *testing.T) {
	path := writeEngineConfigFile(t, `
engine:
  leaseDuration: 10s
  heartbeatInterval: 10s
`)
	if _, err := LoadEngine(path); err == nil {
		t.Fatal("LoadEngine() returned no error")
	}
}

func writeConfigFile(t *testing.T, contents string) string {
	t.Helper()
	t.Setenv("TEST_DATABASE_URL", "postgres://battlefield:secret@localhost:5432/battlefield")
	contents += `
database:
  urlEnvironmentVariable: TEST_DATABASE_URL
  connectTimeout: 5s
  maxConnections: 10
  minConnections: 1
  maxConnectionLifetime: 30m
`

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	return path
}

func writeEngineConfigFile(t *testing.T, contents string) string {
	t.Helper()
	t.Setenv("TEST_DATABASE_URL", "postgres://battlefield:secret@localhost:5432/battlefield")
	contents += `
database:
  urlEnvironmentVariable: TEST_DATABASE_URL
  connectTimeout: 5s
  maxConnections: 10
  minConnections: 1
  maxConnectionLifetime: 30m
`
	path := filepath.Join(t.TempDir(), "engine.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write engine config: %v", err)
	}
	return path
}
