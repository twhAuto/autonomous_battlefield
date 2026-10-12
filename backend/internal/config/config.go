// Loads shared configuration and coordinates process-specific validation.
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"go.yaml.in/yaml/v3"
)

type Config struct {
	HTTP        HTTPServer
	Database    Database
	API         API
	Auth        authn.Config
	ResultCache ResultCache
	Engine      Engine
	AI          AI
}

type API struct {
	ScenarioCatalogPath string
	RulesetCatalogPath  string
	MaxTicks            uint64
}

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

func Load(path string) (Config, error) {
	return LoadAPI(path)
}

func LoadAPI(path string) (Config, error) {
	raw, err := decode(path)
	if err != nil {
		return Config{}, err
	}

	httpConfig, err := parseHTTPConfig(raw)
	if err != nil {
		return Config{}, fmt.Errorf("validate config file %q: %w", path, err)
	}
	databaseConfig, err := parseDatabaseConfig(raw)
	if err != nil {
		return Config{}, fmt.Errorf("validate config file %q: %w", path, err)
	}
	apiConfig, err := parseAPIConfig(raw, path)
	if err != nil {
		return Config{}, fmt.Errorf("validate config file %q: %w", path, err)
	}
	authConfig, err := parseAuthConfig(raw)
	if err != nil {
		return Config{}, fmt.Errorf("validate config file %q: %w", path, err)
	}
	aiConfig, err := parseAIConfig(raw)
	if err != nil {
		return Config{}, fmt.Errorf("validate config file %q: %w", path, err)
	}
	cache, err := parseResultCacheConfig(raw)
	if err != nil {
		return Config{}, err
	}
	return Config{ResultCache: cache, HTTP: httpConfig, Database: databaseConfig, API: apiConfig, Auth: authConfig, AI: aiConfig}, nil
}

func environmentVariableName(configured, fallback string) string {
	if name := strings.TrimSpace(configured); name != "" {
		return name
	}
	return fallback
}

func parseBooleanEnvironmentVariable(name string, defaultValue bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("environment variable %q must be true or false", name)
	}
	return parsed, nil
}

func LoadEngine(path string) (Config, error) {
	raw, err := decode(path)
	if err != nil {
		return Config{}, err
	}
	databaseConfig, err := parseDatabaseConfig(raw)
	if err != nil {
		return Config{}, fmt.Errorf("validate config file %q: %w", path, err)
	}
	engineConfig, err := parseEngineConfig(raw)
	if err != nil {
		return Config{}, fmt.Errorf("validate config file %q: %w", path, err)
	}
	aiConfig, err := parseAIConfig(raw)
	if err != nil {
		return Config{}, fmt.Errorf("validate config file %q: %w", path, err)
	}
	return Config{Database: databaseConfig, Engine: engineConfig, AI: aiConfig}, nil
}

func LoadAgent(path string) (Config, error) {
	raw, err := decode(path)
	if err != nil {
		return Config{}, err
	}
	aiConfig, err := parseAIConfig(raw)
	if err != nil {
		return Config{}, fmt.Errorf("validate config file %q: %w", path, err)
	}
	aiConfig.AgentSecret = os.Getenv("BATTLEFIELD_AGENT_SECRET")
	if len(aiConfig.AgentSecret) < 32 {
		return Config{}, fmt.Errorf("agent secret must contain at least 32 bytes")
	}
	aiConfig.CapabilitySecret = ""

	return Config{AI: aiConfig}, nil
}

func LoadAIGateway(path string) (Config, error) {
	raw, err := decode(path)
	if err != nil {
		return Config{}, err
	}
	databaseConfig, err := parseDatabaseConfig(raw)
	if err != nil {
		return Config{}, fmt.Errorf("validate config file %q: %w", path, err)
	}
	aiConfig, err := parseAIConfig(raw)
	if err != nil {
		return Config{}, fmt.Errorf("validate config file %q: %w", path, err)
	}
	if len(aiConfig.CapabilitySecret) < 32 || aiConfig.CapabilitySecret == localDevelopmentCapabilitySecret {
		return Config{}, fmt.Errorf("capability secret environment variable %q must contain an independent secret of at least 32 bytes", aiConfig.CapabilitySecretEnv)
	}
	aiConfig.AgentSecret = os.Getenv("BATTLEFIELD_AGENT_SECRET")
	if len(aiConfig.AgentSecret) < 32 || aiConfig.AgentSecret == aiConfig.CapabilitySecret {
		return Config{}, fmt.Errorf("the AI gateway requires a distinct agent secret of at least 32 bytes")
	}
	return Config{Database: databaseConfig, AI: aiConfig}, nil
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

func parseAPIConfig(raw fileConfig, path string) (API, error) {
	catalogPath := strings.TrimSpace(raw.API.ScenarioCatalogPath)
	if catalogPath == "" {
		catalogPath = "scenarios"
	}
	if !filepath.IsAbs(catalogPath) {
		catalogPath = filepath.Join(filepath.Dir(path), catalogPath)
	}
	rulesetPath := strings.TrimSpace(raw.API.RulesetCatalogPath)
	if rulesetPath == "" {
		rulesetPath = "rulesets"
	}
	if !filepath.IsAbs(rulesetPath) {
		rulesetPath = filepath.Join(filepath.Dir(path), rulesetPath)
	}
	maxTicks := raw.API.MaxTicks
	if maxTicks == 0 {
		maxTicks = 10_000
	}
	if maxTicks > 10_000 {
		return API{}, fmt.Errorf("api.maxTicks must not exceed 10000")
	}
	return API{ScenarioCatalogPath: catalogPath, RulesetCatalogPath: rulesetPath, MaxTicks: maxTicks}, nil
}

func parseOptionalDuration(field, value string, fallback time.Duration, allowZero bool) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", field, err)
	}
	if duration < 0 || (!allowZero && duration == 0) {
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
