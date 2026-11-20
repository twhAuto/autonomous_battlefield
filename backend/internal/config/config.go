// Loads each backend binary's configuration from one strict YAML file and the
// environment. Every loader returns only the sections its binary uses, so a binary can
// neither read a section it never parsed nor fail on settings that belong to another.
package config

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/generation"
	"go.yaml.in/yaml/v3"
)

// APIConfig configures the API server.
type APIConfig struct {
	HTTP        HTTPServer
	Database    Database
	API         API
	Auth        authn.Config
	ResultCache ResultCache
	Models      Models
	Generation  generation.Config
	Members     Members
}

// EngineConfig configures the battle engine.
type EngineConfig struct {
	Database        Database
	Engine          Engine
	DecisionTimeout time.Duration
}

// AgentConfig configures the outbound model agent.
type AgentConfig struct {
	// Production requires Linux process isolation for CLI credentials.
	Production bool
	Models     Models
	Agent      Agent
}

// AIGatewayConfig configures the AI gateway.
type AIGatewayConfig struct {
	Database Database
	Gateway  Gateway
}

type API struct {
	MaxTicks uint64
	// EngineConcurrency is the engine's battle slots (engine.concurrency), which the
	// league scheduler fills.
	EngineConcurrency int
}

// fileConfig mirrors the YAML file. The *EnvironmentVariable keys are retired: they may
// only repeat the fixed variable name (see checkLegacyVariableKeys).
type fileConfig struct {
	ResultCache struct {
		URLEnvironmentVariable string `yaml:"urlEnvironmentVariable"`
		TTL                    string `yaml:"ttl"`
		Timeout                string `yaml:"timeout"`
	} `yaml:"resultCache"`
	HTTP struct {
		Address           string `yaml:"address"`
		ReadHeaderTimeout string `yaml:"readHeaderTimeout"`
		ReadTimeout       string `yaml:"readTimeout"`
		WriteTimeout      string `yaml:"writeTimeout"`
		IdleTimeout       string `yaml:"idleTimeout"`
		ShutdownTimeout   string `yaml:"shutdownTimeout"`
	} `yaml:"http"`
	Database struct {
		URLEnvironmentVariable string `yaml:"urlEnvironmentVariable"`
		ConnectTimeout         string `yaml:"connectTimeout"`
		MaxConnections         int32  `yaml:"maxConnections"`
		MinConnections         int32  `yaml:"minConnections"`
		MaxConnectionLifetime  string `yaml:"maxConnectionLifetime"`
	} `yaml:"database"`
	API struct {
		// Retired: game content is embedded (see checkRetiredCatalogPaths).
		ScenarioCatalogPath string `yaml:"scenarioCatalogPath"`
		RulesetCatalogPath  string `yaml:"rulesetCatalogPath"`
		MaxTicks            uint64 `yaml:"maxTicks"`
	} `yaml:"api"`
	Auth struct {
		EnabledEnvironmentVariable       string `yaml:"enabledEnvironmentVariable"`
		UsernameEnvironmentVariable      string `yaml:"usernameEnvironmentVariable"`
		PasswordEnvironmentVariable      string `yaml:"passwordEnvironmentVariable"`
		SessionSecretEnvironmentVariable string `yaml:"sessionSecretEnvironmentVariable"`
		CookieSecureEnvironmentVariable  string `yaml:"cookieSecureEnvironmentVariable"`
		SessionTTL                       string `yaml:"sessionTTL"`
	} `yaml:"auth"`
	Engine struct {
		KnowledgeRetrieval struct {
			MaxUnitCards *int `yaml:"maxUnitCards"`
			MaxBytes     *int `yaml:"maxBytes"`
		} `yaml:"knowledgeRetrieval"`
		HealthAddress               string `yaml:"healthAddress"`
		PollInterval                string `yaml:"pollInterval"`
		LeaseDuration               string `yaml:"leaseDuration"`
		HeartbeatInterval           string `yaml:"heartbeatInterval"`
		ShutdownTimeout             string `yaml:"shutdownTimeout"`
		MaxAttempts                 int    `yaml:"maxAttempts"`
		Concurrency                 *int   `yaml:"concurrency"`
		WorkerIDEnvironmentVariable string `yaml:"workerIdEnvironmentVariable"`
	} `yaml:"engine"`
	AI struct {
		BackgroundGeneration struct {
			Enabled          *bool  `yaml:"enabled"`
			ProfileID        string `yaml:"profileId"`
			DailyAllowance   *int   `yaml:"dailyAllowance"`
			Cooldown         string `yaml:"cooldown"`
			QueueTimeout     string `yaml:"queueTimeout"`
			ExecutionTimeout string `yaml:"executionTimeout"`
		} `yaml:"backgroundGeneration"`
		Environment        string `yaml:"environment"`
		AgentConcurrency   *int   `yaml:"agentConcurrency"`
		AgentHealthAddress string `yaml:"agentHealthAddress"`
		AIGatewayAddress   string `yaml:"aiGatewayAddress"`
		// LegacyMCPAddress is the pre-rename key for aiGatewayAddress. Deployed
		// ConfigMaps keep it until every rollback target accepts the new key.
		LegacyMCPAddress                    string            `yaml:"mcpAddress"`
		MCPURL                              string            `yaml:"mcpUrl"`
		DecisionTimeout                     string            `yaml:"decisionTimeout"`
		CapabilitySecretEnvironmentVariable string            `yaml:"capabilitySecretEnvironmentVariable"`
		AllowedOrigins                      []string          `yaml:"allowedOrigins"`
		Profiles                            []ai.ModelProfile `yaml:"profiles"`
	} `yaml:"ai"`
}

func LoadAPI(path string) (APIConfig, error) {
	return load(path, func(raw fileConfig, production bool) (c APIConfig, err error) {
		if c.HTTP, err = parseHTTPConfig(raw); err != nil {
			return c, err
		}
		if c.Database, err = parseDatabaseConfig(raw); err != nil {
			return c, err
		}
		if c.API, err = parseAPIConfig(raw); err != nil {
			return c, err
		}
		if c.Auth, err = parseAuthConfig(raw, production); err != nil {
			return c, err
		}
		if c.ResultCache, err = parseResultCacheConfig(raw); err != nil {
			return c, err
		}
		if c.Models, err = parseModels(raw, production); err != nil {
			return c, err
		}
		if c.Members, err = parseMembersConfig(c.Auth); err != nil {
			return c, err
		}
		c.Generation, err = parseBackgroundGeneration(raw)
		return c, err
	})
}

func LoadEngine(path string) (EngineConfig, error) {
	return load(path, func(raw fileConfig, _ bool) (c EngineConfig, err error) {
		if c.Database, err = parseDatabaseConfig(raw); err != nil {
			return c, err
		}
		if c.Engine, err = parseEngineConfig(raw); err != nil {
			return c, err
		}
		c.DecisionTimeout, err = parseDecisionTimeout(raw)
		return c, err
	})
}

func LoadAgent(path string) (AgentConfig, error) {
	return load(path, func(raw fileConfig, production bool) (c AgentConfig, err error) {
		c.Production = production
		if c.Models, err = parseModels(raw, production); err != nil {
			return c, err
		}
		c.Agent, err = parseAgent(raw)
		return c, err
	})
}

func LoadAIGateway(path string) (AIGatewayConfig, error) {
	return load(path, func(raw fileConfig, _ bool) (c AIGatewayConfig, err error) {
		if c.Database, err = parseDatabaseConfig(raw); err != nil {
			return c, err
		}
		c.Gateway, err = parseGateway(raw)
		return c, err
	})
}

// load decodes the file, validates what every binary shares, and attributes any
// validation error to the file.
func load[T any](path string, parse func(raw fileConfig, production bool) (T, error)) (T, error) {
	var zero T
	raw, err := decode(path)
	if err != nil {
		return zero, err
	}
	production, err := runtimeProduction()
	if err == nil {
		err = checkLegacyVariableKeys(raw)
	}
	if err == nil {
		err = checkRetiredCatalogPaths(raw)
	}
	if err != nil {
		return zero, fmt.Errorf("validate config file %q: %w", path, err)
	}
	configuration, err := parse(raw, production)
	if err != nil {
		return zero, fmt.Errorf("validate config file %q: %w", path, err)
	}
	return configuration, nil
}

func stringOrDefault(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}

func decode(path string) (fileConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return fileConfig{}, fmt.Errorf("open config file %q: %w", path, err)
	}
	defer file.Close()

	var raw fileConfig
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return fileConfig{}, fmt.Errorf("decode config file %q: %w", path, err)
	}

	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return fileConfig{}, fmt.Errorf("config file %q must contain one YAML document", path)
		}
		return fileConfig{}, fmt.Errorf("decode trailing content in config file %q: %w", path, err)
	}
	return raw, nil
}

// retiredCatalogPaths are the only accepted values of the retired catalog path keys:
// where images built before game content was embedded keep it. The base ConfigMap
// keeps them so an image-only rollback to such an image still finds its content,
// since those images resolve a missing path beside /etc/battlefield/config.yaml.
var retiredCatalogPaths = []struct{ field, fixed string }{
	{"api.scenarioCatalogPath", "/app/scenarios"},
	{"api.rulesetCatalogPath", "/app/rulesets"},
}

// checkRetiredCatalogPaths rejects any other value, which current binaries would
// silently ignore.
func checkRetiredCatalogPaths(raw fileConfig) error {
	for i, value := range []string{raw.API.ScenarioCatalogPath, raw.API.RulesetCatalogPath} {
		key := retiredCatalogPaths[i]
		if value := strings.TrimSpace(value); value != "" && value != key.fixed {
			return fmt.Errorf("%s is retired because game content is embedded; remove it (older images use only %s)", key.field, key.fixed)
		}
	}
	return nil
}

func parseAPIConfig(raw fileConfig) (API, error) {
	maxTicks := raw.API.MaxTicks
	if maxTicks == 0 {
		maxTicks = 10_000
	}
	if maxTicks > 10_000 {
		return API{}, fmt.Errorf("api.maxTicks must not exceed 10000")
	}
	concurrency, err := parseEngineConcurrency(raw)
	return API{MaxTicks: maxTicks, EngineConcurrency: concurrency}, err
}

func parseOptionalDuration(field, value string, fallback time.Duration) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", field, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", field)
	}
	return duration, nil
}

func parseDuration(field, value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("%s must not be empty", field)
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", field, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", field)
	}

	return duration, nil
}
