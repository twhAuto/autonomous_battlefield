// Strictly decodes YAML and environment-backed settings, applying defaults and validation
// for each backend process.

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
	"github.com/twh09/autonomous_battlefield/backend/internal/generation"
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

type ResultCache struct {
	URL     string
	TTL     time.Duration
	Timeout time.Duration
}

type API struct {
	ScenarioCatalogPath string
	RulesetCatalogPath  string
	MaxTicks            uint64
}

type Engine struct {
	KnowledgeRetrieval          ai.KnowledgeLimits
	HealthAddress               string
	PollInterval                time.Duration
	LeaseDuration               time.Duration
	HeartbeatInterval           time.Duration
	ShutdownTimeout             time.Duration
	MaxAttempts                 int
	WorkerIDEnvironmentVariable string
}

type AI struct {
	BackgroundGeneration generation.Config
	Environment          string
	AgentHealthAddress   string
	MCPAddress           string
	MCPURL               string
	DecisionTimeout      time.Duration
	CapabilitySecret     string
	CapabilitySecretEnv  string
	AllowedOrigins       []string
	Profiles             []ai.ModelProfile
}

type Database struct {
	URL                   string
	ConnectTimeout        time.Duration
	MaxConnections        int32
	MinConnections        int32
	MaxConnectionLifetime time.Duration
}

type HTTPServer struct {
	Address           string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
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
		Environment                         string            `yaml:"environment"`
		AgentHealthAddress                  string            `yaml:"agentHealthAddress"`
		MCPAddress                          string            `yaml:"mcpAddress"`
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

func parseAuthConfig(raw fileConfig) (authn.Config, error) {
	enabledEnv := environmentVariableName(raw.Auth.EnabledEnvironmentVariable, "BATTLEFIELD_AUTH_ENABLED")
	enabled, err := parseBooleanEnvironmentVariable(enabledEnv, false)
	if err != nil {
		return authn.Config{}, err
	}
	ttl, err := parseOptionalDuration("auth.sessionTTL", raw.Auth.SessionTTL, 12*time.Hour, false)
	if err != nil {
		return authn.Config{}, err
	}
	secureEnv := environmentVariableName(raw.Auth.CookieSecureEnvironmentVariable, "BATTLEFIELD_AUTH_COOKIE_SECURE")
	cookieSecure, err := parseBooleanEnvironmentVariable(secureEnv, false)
	if err != nil {
		return authn.Config{}, err
	}
	if !enabled {
		return authn.Config{SessionTTL: ttl, CookieSecure: cookieSecure}, nil
	}
	usernameEnv := environmentVariableName(raw.Auth.UsernameEnvironmentVariable, "BATTLEFIELD_ADMIN_USERNAME")
	passwordEnv := environmentVariableName(raw.Auth.PasswordEnvironmentVariable, "BATTLEFIELD_ADMIN_PASSWORD")
	secretEnv := environmentVariableName(raw.Auth.SessionSecretEnvironmentVariable, "BATTLEFIELD_SESSION_SECRET")
	return loadAuthenticationSecrets(usernameEnv, passwordEnv, secretEnv, ttl, cookieSecure)
}

func environmentVariableName(configured, fallback string) string {
	if name := strings.TrimSpace(configured); name != "" {
		return name
	}
	return fallback
}

func loadAuthenticationSecrets(usernameEnv, passwordEnv, secretEnv string, ttl time.Duration, cookieSecure bool) (authn.Config, error) {
	username := strings.TrimSpace(os.Getenv(usernameEnv))
	password := os.Getenv(passwordEnv)
	secret := os.Getenv(secretEnv)
	if username == "" {
		return authn.Config{}, fmt.Errorf("authentication username environment variable %q must not be empty", usernameEnv)
	}
	if len(password) < 12 {
		return authn.Config{}, fmt.Errorf("authentication password environment variable %q must contain at least 12 characters", passwordEnv)
	}
	if len(secret) < 32 {
		return authn.Config{}, fmt.Errorf("authentication session secret environment variable %q must contain at least 32 characters", secretEnv)
	}
	return authn.Config{
		Enabled: true, Username: username, Password: password, SessionSecret: secret,
		SessionTTL: ttl, CookieSecure: cookieSecure,
	}, nil
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
	if aiConfig.CapabilitySecret == "" {
		return Config{}, fmt.Errorf("capability secret environment variable %q must not be empty", aiConfig.CapabilitySecretEnv)
	}
	return Config{AI: aiConfig}, nil
}

func LoadMCP(path string) (Config, error) {
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
	if aiConfig.CapabilitySecret == "" {
		return Config{}, fmt.Errorf("capability secret environment variable %q must not be empty", aiConfig.CapabilitySecretEnv)
	}
	return Config{Database: databaseConfig, AI: aiConfig}, nil
}

func parseAIConfig(raw fileConfig) (AI, error) {
	environment, err := parseAIEnvironment(raw.AI.Environment)
	if err != nil {
		return AI{}, err
	}
	health := stringOrDefault(raw.AI.AgentHealthAddress, ":8083")
	mcpAddress := stringOrDefault(raw.AI.MCPAddress, ":8084")
	mcpURL := stringOrDefault(raw.AI.MCPURL, "http://127.0.0.1:8084/mcp")
	timeout, err := parseOptionalDuration("ai.decisionTimeout", raw.AI.DecisionTimeout, 2*time.Minute, false)
	if err != nil {
		return AI{}, err
	}
	secret, secretEnv := aiCapabilitySecret(raw.AI.CapabilitySecretEnvironmentVariable, environment)
	if err := validateAIProfiles(raw.AI.Profiles); err != nil {
		return AI{}, err
	}
	gen, err := parseBackgroundGeneration(raw)
	if err != nil {
		return AI{}, err
	}
	return AI{BackgroundGeneration: gen, Environment: environment, AgentHealthAddress: health, MCPAddress: mcpAddress, MCPURL: mcpURL,
		DecisionTimeout: timeout, CapabilitySecret: secret, CapabilitySecretEnv: secretEnv,
		AllowedOrigins: append([]string(nil), raw.AI.AllowedOrigins...), Profiles: append([]ai.ModelProfile(nil), raw.AI.Profiles...)}, nil
}

func parseAIEnvironment(value string) (string, error) {
	environment := stringOrDefault(value, "local")
	if environment != "local" && environment != "cluster" {
		return "", fmt.Errorf("ai.environment must be local or cluster")
	}
	return environment, nil
}

func stringOrDefault(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}

func aiCapabilitySecret(configuredEnvironmentVariable, environment string) (string, string) {
	secretEnv := stringOrDefault(configuredEnvironmentVariable, "BATTLEFIELD_MCP_CAPABILITY_SECRET")
	secret := strings.TrimSpace(os.Getenv(secretEnv))
	if secret == "" && environment == "local" {
		secret = "local-development-capability-secret-change-me"
	}
	return secret, secretEnv
}

func validateAIProfiles(profiles []ai.ModelProfile) error {
	seen := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		id := strings.TrimSpace(profile.ID)
		if id == "" || seen[id] {
			return fmt.Errorf("ai profile IDs must be unique and non-empty")
		}
		seen[id] = true
		if err := validateAIProfile(profile); err != nil {
			return err
		}
	}
	return nil
}

func validateAIProfile(profile ai.ModelProfile) error {
	if profile.MaxSteps < 1 || profile.MaxSteps > 10 {
		return fmt.Errorf("ai profile %q maxSteps must be between 1 and 10", profile.ID)
	}
	if profile.Runtime == "api" && profile.MaxOutputTokens < 1 {
		return fmt.Errorf("ai profile %q maxOutputTokens must be positive", profile.ID)
	}
	if profile.Runtime == "cli" && strings.TrimSpace(profile.SupportedVersionPrefix) == "" {
		return fmt.Errorf("ai profile %q supportedVersionPrefix is required", profile.ID)
	}
	if profile.Runtime == "cli" && strings.TrimSpace(profile.Model) == "" {
		return fmt.Errorf("ai profile %q model is required", profile.ID)
	}
	return nil
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

func parseEngineConfig(raw fileConfig) (Engine, error) {
	healthAddress := strings.TrimSpace(raw.Engine.HealthAddress)
	if healthAddress == "" {
		healthAddress = ":8081"
	}
	pollInterval, err := parseOptionalDuration("engine.pollInterval", raw.Engine.PollInterval, time.Second, false)
	if err != nil {
		return Engine{}, err
	}
	leaseDuration, err := parseOptionalDuration("engine.leaseDuration", raw.Engine.LeaseDuration, 30*time.Second, false)
	if err != nil {
		return Engine{}, err
	}
	heartbeatInterval, err := parseOptionalDuration("engine.heartbeatInterval", raw.Engine.HeartbeatInterval, 10*time.Second, false)
	if err != nil {
		return Engine{}, err
	}
	shutdownTimeout, err := parseOptionalDuration("engine.shutdownTimeout", raw.Engine.ShutdownTimeout, 10*time.Second, false)
	if err != nil {
		return Engine{}, err
	}
	if heartbeatInterval >= leaseDuration {
		return Engine{}, fmt.Errorf("engine.heartbeatInterval must be less than engine.leaseDuration")
	}
	maxAttempts := raw.Engine.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 3
	}
	if maxAttempts < 1 {
		return Engine{}, fmt.Errorf("engine.maxAttempts must be positive")
	}
	workerEnv := strings.TrimSpace(raw.Engine.WorkerIDEnvironmentVariable)
	if workerEnv == "" {
		workerEnv = "ENGINE_WORKER_ID"
	}
	limits := ai.DefaultKnowledgeLimits()
	if raw.Engine.KnowledgeRetrieval.MaxUnitCards != nil {
		limits.MaxUnitCards = *raw.Engine.KnowledgeRetrieval.MaxUnitCards
	}
	if raw.Engine.KnowledgeRetrieval.MaxBytes != nil {
		limits.MaxBytes = *raw.Engine.KnowledgeRetrieval.MaxBytes
	}
	if err := limits.Validate(); err != nil {
		return Engine{}, err
	}
	return Engine{KnowledgeRetrieval: limits,
		HealthAddress: healthAddress, PollInterval: pollInterval, LeaseDuration: leaseDuration,
		HeartbeatInterval: heartbeatInterval, ShutdownTimeout: shutdownTimeout,
		MaxAttempts: maxAttempts, WorkerIDEnvironmentVariable: workerEnv,
	}, nil
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

func parseDatabaseConfig(raw fileConfig) (Database, error) {
	urlEnvironmentVariable := strings.TrimSpace(raw.Database.URLEnvironmentVariable)
	if urlEnvironmentVariable == "" {
		return Database{}, fmt.Errorf("database.urlEnvironmentVariable must not be empty")
	}

	url := strings.TrimSpace(os.Getenv(urlEnvironmentVariable))
	if url == "" {
		return Database{}, fmt.Errorf("database connection URL environment variable %q must not be empty", urlEnvironmentVariable)
	}

	connectTimeout, err := parseDuration("database.connectTimeout", raw.Database.ConnectTimeout)
	if err != nil {
		return Database{}, err
	}
	maxConnectionLifetime, err := parseDuration("database.maxConnectionLifetime", raw.Database.MaxConnectionLifetime)
	if err != nil {
		return Database{}, err
	}
	if raw.Database.MaxConnections <= 0 {
		return Database{}, fmt.Errorf("database.maxConnections must be greater than zero")
	}
	if raw.Database.MinConnections < 0 {
		return Database{}, fmt.Errorf("database.minConnections must not be negative")
	}
	if raw.Database.MinConnections > raw.Database.MaxConnections {
		return Database{}, fmt.Errorf("database.minConnections must not exceed database.maxConnections")
	}

	return Database{
		URL:                   url,
		ConnectTimeout:        connectTimeout,
		MaxConnections:        raw.Database.MaxConnections,
		MinConnections:        raw.Database.MinConnections,
		MaxConnectionLifetime: maxConnectionLifetime,
	}, nil
}

func parseHTTPConfig(raw fileConfig) (HTTPServer, error) {
	address := strings.TrimSpace(raw.HTTP.Address)
	if address == "" {
		return HTTPServer{}, fmt.Errorf("http.address must not be empty")
	}

	readHeaderTimeout, err := parseDuration("http.readHeaderTimeout", raw.HTTP.ReadHeaderTimeout)
	if err != nil {
		return HTTPServer{}, err
	}
	readTimeout, err := parseDuration("http.readTimeout", raw.HTTP.ReadTimeout)
	if err != nil {
		return HTTPServer{}, err
	}
	writeTimeout, err := parseDuration("http.writeTimeout", raw.HTTP.WriteTimeout)
	if err != nil {
		return HTTPServer{}, err
	}
	idleTimeout, err := parseDuration("http.idleTimeout", raw.HTTP.IdleTimeout)
	if err != nil {
		return HTTPServer{}, err
	}
	shutdownTimeout, err := parseDuration("http.shutdownTimeout", raw.HTTP.ShutdownTimeout)
	if err != nil {
		return HTTPServer{}, err
	}

	return HTTPServer{
		Address:           address,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ShutdownTimeout:   shutdownTimeout,
	}, nil
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

func parseResultCacheConfig(raw fileConfig) (ResultCache, error) {
	ttl, err := parseOptionalDuration("resultCache.ttl", raw.ResultCache.TTL, time.Hour, false)
	if err != nil {
		return ResultCache{}, err
	}
	timeout, err := parseOptionalDuration("resultCache.timeout", raw.ResultCache.Timeout, 100*time.Millisecond, false)
	if err != nil {
		return ResultCache{}, err
	}
	return ResultCache{URL: strings.TrimSpace(os.Getenv(environmentVariableName(raw.ResultCache.URLEnvironmentVariable, "BATTLEFIELD_REDIS_URL"))), TTL: ttl, Timeout: timeout}, nil
}

func parseBackgroundGeneration(raw fileConfig) (generation.Config, error) {
	c := generation.DefaultConfig()
	v := raw.AI.BackgroundGeneration
	if v.Enabled != nil {
		c.Enabled = *v.Enabled
	}
	if v.ProfileID != "" {
		c.ProfileID = v.ProfileID
	}
	if v.DailyAllowance != nil {
		c.DailyAllowance = *v.DailyAllowance
	}
	var err error
	c.Cooldown, err = parseOptionalDuration("ai.backgroundGeneration.cooldown", v.Cooldown, c.Cooldown, false)
	if err != nil {
		return c, err
	}
	c.QueueTimeout, err = parseOptionalDuration("ai.backgroundGeneration.queueTimeout", v.QueueTimeout, c.QueueTimeout, false)
	if err != nil {
		return c, err
	}
	c.ExecutionTimeout, err = parseOptionalDuration("ai.backgroundGeneration.executionTimeout", v.ExecutionTimeout, c.ExecutionTimeout, false)
	if err != nil {
		return c, err
	}
	return c, c.Validate()
}
