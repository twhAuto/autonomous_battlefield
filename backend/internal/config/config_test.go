// Checks strict backend configuration loading, process-specific defaults, durations,
// authentication, and AI profile validation.

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

	configuration, err := LoadAPI(path)
	if err != nil {
		t.Fatalf("LoadAPI() returned an error: %v", err)
	}

	if configuration.HTTP.Address != ":9000" {
		t.Fatalf("Address = %q, want %q", configuration.HTTP.Address, ":9000")
	}
	if configuration.HTTP.ReadTimeout != 3*time.Second {
		t.Fatalf("ReadTimeout = %v, want %v", configuration.HTTP.ReadTimeout, 3*time.Second)
	}
	if configuration.API.MaxTicks != 10_000 {
		t.Fatalf("MaxTicks = %d, want the 10000 default", configuration.API.MaxTicks)
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

	_, err := LoadAPI(path)
	if err == nil {
		t.Fatal("LoadAPI() returned no error")
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

	_, err := LoadAPI(path)
	if err == nil {
		t.Fatal("LoadAPI() returned no error")
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
  sessionTTL: 4h
`)
	setLocalPasswordEnvironment(t)

	configuration, err := LoadAPI(path)
	if err != nil {
		t.Fatalf("LoadAPI() returned an error: %v", err)
	}
	if !configuration.Auth.Enabled || configuration.Auth.Username != "admin" || configuration.Auth.SessionTTL != 4*time.Hour || !configuration.Auth.CookieSecure {
		t.Fatalf("Auth = %#v", configuration.Auth)
	}
}

func TestAuthenticationConfigurationValidation(t *testing.T) {
	setLocalPasswordEnvironment(t)
	if _, err := parseAuthConfig(fileConfig{}, false); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name  string
		env   string
		value string
	}{
		{name: "enabled flag", env: envAuthEnabled, value: "sometimes"},
		{name: "username", env: envAdminUsername, value: ""},
		{name: "password", env: envAdminPassword, value: "short"},
		{name: "example password", env: envAdminPassword, value: "replace-with-at-least-12-characters"},
		{name: "session secret", env: envSessionSecret, value: "short"},
		{name: "example session secret", env: envSessionSecret, value: "replace-with-at-least-32-random-characters"},
		{name: "secure flag", env: envCookieSecure, value: "sometimes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.env, test.value)
			if _, err := parseAuthConfig(fileConfig{}, false); err == nil {
				t.Fatal("invalid authentication configuration accepted")
			}
		})
	}

	t.Setenv(envAuthEnabled, "false")
	disabled, err := parseAuthConfig(fileConfig{}, false)
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
		raw.Database.ConnectTimeout = "1s"
		raw.Database.MaxConnections = 2
		raw.Database.MinConnections = 1
		raw.Database.MaxConnectionLifetime = "1m"
		return raw
	}
	t.Setenv(envDatabaseURL, "postgres://localhost/database")

	tests := []struct {
		name   string
		mutate func(*fileConfig)
	}{
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

	t.Setenv(envDatabaseURL, "")
	if _, err := parseDatabaseConfig(valid()); err == nil {
		t.Fatal("parseDatabaseConfig() accepted an empty database URL")
	}
}

func TestParseEngineConfigValidationAndDefaults(t *testing.T) {
	t.Setenv(envEngineWorkerID, "")
	configuration, err := parseEngineConfig(fileConfig{})
	if err != nil {
		t.Fatalf("parseEngineConfig() returned an error: %v", err)
	}
	if configuration.HealthAddress != ":8081" || configuration.MaxAttempts != 3 || !strings.Contains(configuration.WorkerID, "-") {
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

func TestEngineWorkerIDUsesEnvironmentOrHost(t *testing.T) {
	t.Setenv(envEngineWorkerID, " configured-worker ")
	if id := workerID(); id != "configured-worker" {
		t.Fatalf("workerID() = %q, want configured-worker", id)
	}
	t.Setenv(envEngineWorkerID, "")
	if id := workerID(); !strings.Contains(id, "-") {
		t.Fatalf("workerID() fallback = %q, want hostname-pid", id)
	}
}

func TestParseModelsDefaultsProfilesAndValidation(t *testing.T) {
	configuration, err := parseModels(fileConfig{}, false)
	if err != nil || configuration.Environment != "local" || configuration.DecisionTimeout != 2*time.Minute {
		t.Fatalf("Models=%#v err=%v", configuration, err)
	}
	valid := func() fileConfig {
		var raw fileConfig
		raw.AI.Environment = "cluster"
		raw.AI.DecisionTimeout = "2s"
		raw.AI.Profiles = []ai.ModelProfile{{ID: "profile", DisplayName: "Profile", Provider: "openai", Runtime: "api", Scope: "cluster", MaxSteps: 3, MaxOutputTokens: 100}}
		return raw
	}
	if parsed, err := parseModels(valid(), false); err != nil || len(parsed.Profiles) != 1 {
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
		if _, err := parseModels(raw, false); err == nil {
			t.Fatal("invalid model configuration accepted")
		}
	}
}

func TestAIGatewayAddressAcceptsLegacyKey(t *testing.T) {
	for _, tc := range []struct{ current, legacy, want string }{
		{"", "", ":8084"},
		{":9000", "", ":9000"},
		{"", ":9001", ":9001"},
		{":9002", ":9002", ":9002"},
	} {
		address, err := aiGatewayAddress(tc.current, tc.legacy)
		if err != nil || address != tc.want {
			t.Fatalf("aiGatewayAddress=%q mcpAddress=%q: got %q, %v; want %q", tc.current, tc.legacy, address, err, tc.want)
		}
	}
	if _, err := aiGatewayAddress(":9000", ":9001"); err == nil {
		t.Fatal("conflicting gateway address keys accepted")
	}
}

func TestLoadAgentAndAIGatewayRequireSecrets(t *testing.T) {
	path := writeEngineConfigFile(t, "ai:\n  environment: cluster\n")
	t.Setenv(envAgentSecret, "")
	t.Setenv(envCapabilitySecret, "")
	if _, err := LoadAgent(path); err == nil {
		t.Fatal("agent accepted missing secret")
	}
	if _, err := LoadAIGateway(path); err == nil {
		t.Fatal("AI gateway accepted missing secret")
	}
	t.Setenv(envAgentSecret, strings.Repeat("w", 32))
	t.Setenv(envCapabilitySecret, "secret")
	agentConfig, err := LoadAgent(path)
	if err != nil || agentConfig.Agent.Secret != strings.Repeat("w", 32) {
		t.Fatalf("agent=%#v err=%v", agentConfig.Agent, err)
	}
	if _, err := LoadAIGateway(path); err == nil {
		t.Fatal("AI gateway accepted a short capability secret")
	}
	t.Setenv(envCapabilitySecret, strings.Repeat("m", 32))
	gateway, err := LoadAIGateway(path)
	if err != nil || gateway.Gateway.CapabilitySecret != strings.Repeat("m", 32) || gateway.Gateway.Address != ":8084" {
		t.Fatalf("gateway=%#v err=%v", gateway.Gateway, err)
	}
}

func TestLoadAIGatewayRejectsExampleAndSharedSecrets(t *testing.T) {
	path := writeEngineConfigFile(t, "ai:\n  environment: local\n")
	for _, tc := range []struct{ agent, capability string }{
		{strings.Repeat("w", 32), ""},
		{strings.Repeat("w", 32), "replace-with-at-least-32-random-characters"},
		{strings.Repeat("w", 32), retiredPublicSecret},
		{"replace-with-at-least-32-random-characters", strings.Repeat("m", 32)},
		{strings.Repeat("s", 32), strings.Repeat("s", 32)},
	} {
		t.Setenv(envAgentSecret, tc.agent)
		t.Setenv(envCapabilitySecret, tc.capability)
		if _, err := LoadAIGateway(path); err == nil {
			t.Fatalf("AI gateway accepted agent=%q capability=%q", tc.agent, tc.capability)
		}
	}
}

func TestEveryLoaderRejectsUnknownRuntimeEnvironment(t *testing.T) {
	apiPath := writeConfigFile(t, validHTTP)
	enginePath := writeEngineConfigFile(t, "")
	t.Setenv(envAgentSecret, strings.Repeat("w", 32))
	t.Setenv(envCapabilitySecret, strings.Repeat("m", 32))
	loaders := map[string]func() error{
		"api":     func() error { _, err := LoadAPI(apiPath); return err },
		"engine":  func() error { _, err := LoadEngine(enginePath); return err },
		"agent":   func() error { _, err := LoadAgent(enginePath); return err },
		"gateway": func() error { _, err := LoadAIGateway(enginePath); return err },
	}
	for name, load := range loaders {
		t.Setenv(envRuntime, "dev")
		if err := load(); err != nil {
			t.Fatalf("%s rejected dev: %v", name, err)
		}
		for _, value := range []string{"prod", "Production", "staging"} {
			t.Setenv(envRuntime, value)
			if err := load(); err == nil || !strings.Contains(err.Error(), envRuntime) {
				t.Fatalf("%s accepted %s=%q (err=%v)", name, envRuntime, value, err)
			}
		}
	}
}

func TestRuntimeProduction(t *testing.T) {
	for value, want := range map[string]bool{"": false, "dev": false, "production": true, " production ": true} {
		t.Setenv(envRuntime, value)
		if got, err := runtimeProduction(); err != nil || got != want {
			t.Fatalf("%q: production=%t err=%v, want %t", value, got, err, want)
		}
	}
}

func TestLegacyVariableKeysMustNameTheFixedVariable(t *testing.T) {
	if _, err := LoadEngine(writeEngineConfigFile(t, "engine:\n  workerIdEnvironmentVariable: ENGINE_WORKER_ID\n")); err != nil {
		t.Fatalf("matching legacy key rejected: %v", err)
	}
	for _, yaml := range []string{
		"engine:\n  workerIdEnvironmentVariable: OTHER_WORKER_ID\n",
		"resultCache:\n  urlEnvironmentVariable: OTHER_REDIS_URL\n",
		"auth:\n  sessionSecretEnvironmentVariable: OTHER_SECRET\n",
		"ai:\n  capabilitySecretEnvironmentVariable: OTHER_CAPABILITY\n",
	} {
		_, err := LoadEngine(writeEngineConfigFile(t, yaml))
		if err == nil || !strings.Contains(err.Error(), "no longer configurable") {
			t.Fatalf("legacy key accepted: %q (err=%v)", yaml, err)
		}
	}
	var raw fileConfig
	raw.Database.URLEnvironmentVariable = "TEST_DATABASE_URL"
	if err := checkLegacyVariableKeys(raw); err == nil {
		t.Fatal("renamed database URL variable accepted")
	}
}

func TestLoadersParseOnlyTheirOwnSections(t *testing.T) {
	// An agent-only setting must not stop the engine or the gateway from starting.
	path := writeEngineConfigFile(t, "ai:\n  agentConcurrency: 9\n")
	t.Setenv(envAgentSecret, strings.Repeat("w", 32))
	t.Setenv(envCapabilitySecret, strings.Repeat("m", 32))
	if _, err := LoadEngine(path); err != nil {
		t.Fatalf("engine rejected an agent setting: %v", err)
	}
	if _, err := LoadAIGateway(path); err != nil {
		t.Fatalf("gateway rejected an agent setting: %v", err)
	}
	if _, err := LoadAgent(path); err == nil {
		t.Fatal("agent accepted an invalid concurrency")
	}
}

func TestLoadAPIAttributesResultCacheErrorsToTheFile(t *testing.T) {
	path := writeConfigFile(t, validHTTP+"resultCache:\n  ttl: 0s\n")
	_, err := LoadAPI(path)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "resultCache.ttl") {
		t.Fatalf("error %v does not name the file and field", err)
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

const validHTTP = "http:\n  address: \":8080\"\n  readHeaderTimeout: 1s\n  readTimeout: 2s\n  writeTimeout: 2s\n  idleTimeout: 30s\n  shutdownTimeout: 1s\n"

func setLocalPasswordEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv(envAuthMode, "")
	t.Setenv(envAuthEnabled, "true")
	t.Setenv(envAdminUsername, "admin")
	t.Setenv(envAdminPassword, "a-long-test-password")
	t.Setenv(envSessionSecret, "a-test-session-secret-with-32-characters")
	t.Setenv(envCookieSecure, "true")
}

// clearSharedEnvironment keeps a developer's or CI runner's settings out of the tests.
func clearSharedEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv(envRuntime, "")
	t.Setenv(envAuthMode, "")
	t.Setenv(envDatabaseURL, "postgres://battlefield:secret@localhost:5432/battlefield")
}

func writeConfigFile(t *testing.T, contents string) string {
	t.Helper()
	clearSharedEnvironment(t)
	if _, ok := os.LookupEnv(envAuthEnabled); !ok {
		t.Setenv(envAuthEnabled, "false")
	}
	if _, ok := os.LookupEnv(envAgentSecret); !ok {
		t.Setenv(envAgentSecret, strings.Repeat("w", 32))
	}
	contents += `
database:
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
	clearSharedEnvironment(t)
	contents += `
database:
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

func TestBackgroundGenerationConfiguration(t *testing.T) {
	const base = validHTTP
	c, err := LoadAPI(writeConfigFile(t, base))
	if err != nil {
		t.Fatal(err)
	}
	g := c.Generation
	if !g.Enabled || g.ProfileID != "codex-subscription-local" || g.DailyAllowance != 10 || g.Cooldown != 30*time.Second || g.QueueTimeout != 5*time.Minute || g.ExecutionTimeout != 2*time.Minute {
		t.Fatal(g)
	}
	c, err = LoadAPI(writeConfigFile(t, base+"ai:\n  backgroundGeneration:\n    enabled: false\n    profileId: custom-writer\n    dailyAllowance: 7\n    cooldown: 45s\n    queueTimeout: 3m\n    executionTimeout: 1m\n"))
	if err != nil {
		t.Fatal(err)
	}
	g = c.Generation
	if g.Enabled || g.ProfileID != "custom-writer" || g.DailyAllowance != 7 || g.Cooldown != 45*time.Second || g.QueueTimeout != 3*time.Minute || g.ExecutionTimeout != time.Minute {
		t.Fatal(g)
	}
	for _, field := range []string{"dailyAllowance: 0", "cooldown: 0s", "cooldown: nonsense", "queueTimeout: -1s", "executionTimeout: nonsense"} {
		if _, err := LoadAPI(writeConfigFile(t, base+"ai:\n  backgroundGeneration:\n    "+field+"\n")); err == nil {
			t.Fatal("invalid generation config accepted", field)
		}
	}
}

func TestAgentConcurrencyConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		want       int
		invalid    bool
	}{
		{"default", "ai: {}", 1, false},
		{"four", "ai: {agentConcurrency: 4}", 4, false},
		{"zero", "ai: {agentConcurrency: 0}", 0, true},
		{"negative", "ai: {agentConcurrency: -1}", 0, true},
		{"too many", "ai: {agentConcurrency: 5}", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadAgent(writeConfigFile(t, tc.yaml))
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid concurrency accepted")
				}
				return
			}
			if err != nil || cfg.Agent.Concurrency != tc.want {
				t.Fatalf("concurrency=%d error=%v", cfg.Agent.Concurrency, err)
			}
		})
	}
}
