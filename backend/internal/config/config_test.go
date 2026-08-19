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

func writeConfigFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	return path
}
