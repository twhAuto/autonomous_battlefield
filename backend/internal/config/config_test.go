package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
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
	if filepath.Base(configuration.API.RulesetCatalogPath) != "rulesets" || filepath.Base(configuration.API.ScenarioCatalogPath) != "scenarios" {
		t.Fatalf("catalog paths = %#v", configuration.API)
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

func TestLoadAuthenticationConfiguration(t *testing.T) {
	path := writeConfigFile(t, `
http:
  address: ":8080"
  readHeaderTimeout: 5s
  readTimeout: 10s
  writeTimeout: 10s
  idleTimeout: 60s
  shutdownTimeout: 10s
auth:
  enabledEnvironmentVariable: TEST_AUTH_ENABLED
  usernameEnvironmentVariable: TEST_AUTH_USERNAME
  passwordEnvironmentVariable: TEST_AUTH_PASSWORD
  sessionSecretEnvironmentVariable: TEST_AUTH_SECRET
  cookieSecureEnvironmentVariable: TEST_AUTH_SECURE
  sessionTTL: 4h
`)
	t.Setenv("TEST_AUTH_ENABLED", "true")
	t.Setenv("TEST_AUTH_USERNAME", "admin")
	t.Setenv("TEST_AUTH_PASSWORD", "a-long-test-password")
	t.Setenv("TEST_AUTH_SECRET", "a-test-session-secret-with-32-characters")
	t.Setenv("TEST_AUTH_SECURE", "true")

	configuration, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned an error: %v", err)
	}
	if !configuration.Auth.Enabled || configuration.Auth.Username != "admin" || configuration.Auth.SessionTTL != 4*time.Hour || !configuration.Auth.CookieSecure {
		t.Fatalf("Auth = %#v", configuration.Auth)
	}
}

func TestAuthenticationConfigurationValidation(t *testing.T) {
	valid := func() fileConfig {
		var raw fileConfig
		raw.Auth.EnabledEnvironmentVariable = "TEST_AUTH_ENABLED"
		raw.Auth.UsernameEnvironmentVariable = "TEST_AUTH_USERNAME"
		raw.Auth.PasswordEnvironmentVariable = "TEST_AUTH_PASSWORD"
		raw.Auth.SessionSecretEnvironmentVariable = "TEST_AUTH_SECRET"
		raw.Auth.CookieSecureEnvironmentVariable = "TEST_AUTH_SECURE"
		return raw
	}
	t.Setenv("TEST_AUTH_ENABLED", "true")
	t.Setenv("TEST_AUTH_USERNAME", "admin")
	t.Setenv("TEST_AUTH_PASSWORD", "a-long-test-password")
	t.Setenv("TEST_AUTH_SECRET", "a-test-session-secret-with-32-characters")
	t.Setenv("TEST_AUTH_SECURE", "true")
	if _, err := parseAuthConfig(valid()); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name  string
		env   string
		value string
	}{
		{name: "enabled flag", env: "TEST_AUTH_ENABLED", value: "sometimes"},
		{name: "username", env: "TEST_AUTH_USERNAME", value: ""},
		{name: "password", env: "TEST_AUTH_PASSWORD", value: "short"},
		{name: "session secret", env: "TEST_AUTH_SECRET", value: "short"},
		{name: "secure flag", env: "TEST_AUTH_SECURE", value: "sometimes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.env, test.value)
			if _, err := parseAuthConfig(valid()); err == nil {
				t.Fatal("invalid authentication configuration accepted")
			}
		})
	}

	t.Setenv("TEST_AUTH_ENABLED", "false")
	disabled, err := parseAuthConfig(valid())
	if err != nil || disabled.Enabled {
		t.Fatalf("disabled auth=%#v err=%v", disabled, err)
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

func TestParseDatabaseConfigValidation(t *testing.T) {
	valid := func() fileConfig {
		var raw fileConfig
		raw.Database.URLEnvironmentVariable = "TEST_DATABASE_URL"
		raw.Database.ConnectTimeout = "1s"
		raw.Database.MaxConnections = 2
		raw.Database.MinConnections = 1
		raw.Database.MaxConnectionLifetime = "1m"
		return raw
	}
	t.Setenv("TEST_DATABASE_URL", "postgres://localhost/database")

	tests := []struct {
		name   string
		mutate func(*fileConfig)
	}{
		{name: "environment variable name", mutate: func(raw *fileConfig) { raw.Database.URLEnvironmentVariable = "" }},
		{name: "connect timeout", mutate: func(raw *fileConfig) { raw.Database.ConnectTimeout = "" }},
		{name: "connection lifetime", mutate: func(raw *fileConfig) { raw.Database.MaxConnectionLifetime = "bad" }},
		{name: "max connections", mutate: func(raw *fileConfig) { raw.Database.MaxConnections = 0 }},
		{name: "negative min connections", mutate: func(raw *fileConfig) { raw.Database.MinConnections = -1 }},
		{name: "min above max", mutate: func(raw *fileConfig) { raw.Database.MinConnections = 3 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := valid()
			test.mutate(&raw)
			if _, err := parseDatabaseConfig(raw); err == nil {
				t.Fatal("parseDatabaseConfig() returned no error")
			}
		})
	}

	t.Setenv("TEST_DATABASE_URL", "")
	if _, err := parseDatabaseConfig(valid()); err == nil {
		t.Fatal("parseDatabaseConfig() accepted an empty database URL")
	}
}

func TestParseEngineConfigValidationAndDefaults(t *testing.T) {
	configuration, err := parseEngineConfig(fileConfig{})
	if err != nil {
		t.Fatalf("parseEngineConfig() returned an error: %v", err)
	}
	if configuration.HealthAddress != ":8081" || configuration.MaxAttempts != 3 || configuration.WorkerIDEnvironmentVariable != "ENGINE_WORKER_ID" {
		t.Fatalf("default engine config = %#v", configuration)
	}

	for _, test := range []struct {
		name   string
		mutate func(*fileConfig)
	}{
		{name: "poll interval", mutate: func(raw *fileConfig) { raw.Engine.PollInterval = "bad" }},
		{name: "lease duration", mutate: func(raw *fileConfig) { raw.Engine.LeaseDuration = "0s" }},
		{name: "heartbeat interval", mutate: func(raw *fileConfig) { raw.Engine.HeartbeatInterval = "-1s" }},
		{name: "shutdown timeout", mutate: func(raw *fileConfig) { raw.Engine.ShutdownTimeout = "bad" }},
		{name: "max attempts", mutate: func(raw *fileConfig) { raw.Engine.MaxAttempts = -1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var raw fileConfig
			test.mutate(&raw)
			if _, err := parseEngineConfig(raw); err == nil {
				t.Fatal("parseEngineConfig() returned no error")
			}
		})
	}
}

func TestParseAIConfigDefaultsProfilesAndValidation(t *testing.T) {
	configuration, err := parseAIConfig(fileConfig{})
	if err != nil || configuration.AgentHealthAddress != ":8083" || configuration.MCPAddress != ":8084" || configuration.DecisionTimeout != 2*time.Minute || configuration.CapabilitySecret == "" {
		t.Fatalf("AI=%#v err=%v", configuration, err)
	}
	valid := func() fileConfig {
		var raw fileConfig
		raw.AI.Environment = "cluster"
		raw.AI.DecisionTimeout = "2s"
		raw.AI.Profiles = []ai.ModelProfile{{ID: "profile", DisplayName: "Profile", Provider: "openai", Runtime: "api", Scope: "cluster", MaxSteps: 3, MaxOutputTokens: 100}}
		return raw
	}
	if parsed, err := parseAIConfig(valid()); err != nil || len(parsed.Profiles) != 1 {
		t.Fatal(err)
	}
	for _, mutate := range []func(*fileConfig){func(raw *fileConfig) { raw.AI.Environment = "remote" }, func(raw *fileConfig) { raw.AI.DecisionTimeout = "bad" }, func(raw *fileConfig) { raw.AI.Profiles[0].MaxSteps = 0 }, func(raw *fileConfig) { raw.AI.Profiles[0].MaxOutputTokens = 0 }, func(raw *fileConfig) { raw.AI.Profiles = append(raw.AI.Profiles, raw.AI.Profiles[0]) }, func(raw *fileConfig) {
		raw.AI.Profiles[0].Runtime = "cli"
		raw.AI.Profiles[0].SupportedVersionPrefix = ""
	}, func(raw *fileConfig) {
		raw.AI.Profiles[0].Runtime = "cli"
		raw.AI.Profiles[0].SupportedVersionPrefix = "codex-cli 0.152."
		raw.AI.Profiles[0].Model = ""
	}} {
		raw := valid()
		mutate(&raw)
		if _, err := parseAIConfig(raw); err == nil {
			t.Fatal("invalid AI config accepted")
		}
	}
}

func TestLoadAgentAndMCPRequireClusterCapability(t *testing.T) {
	path := writeEngineConfigFile(t, `ai:
  environment: cluster
  capabilitySecretEnvironmentVariable: TEST_CAPABILITY_SECRET
`)
	t.Setenv("TEST_CAPABILITY_SECRET", "")
	if _, err := LoadAgent(path); err == nil {
		t.Fatal("agent accepted missing secret")
	}
	if _, err := LoadMCP(path); err == nil {
		t.Fatal("MCP accepted missing secret")
	}
	t.Setenv("TEST_CAPABILITY_SECRET", "secret")
	if _, err := LoadAgent(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMCP(path); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeRejectsMultipleDocumentsAndMissingFile(t *testing.T) {
	if _, err := decode(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("decode() returned no error for a missing file")
	}
	path := filepath.Join(t.TempDir(), "multiple.yaml")
	if err := os.WriteFile(path, []byte("http: {}\n---\ndatabase: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := decode(path); err == nil {
		t.Fatal("decode() accepted multiple YAML documents")
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
