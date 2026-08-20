package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	HTTP     HTTPServer
	Database Database
	API      API
	Engine   Engine
}

type API struct {
	ScenarioCatalogPath string
	MaxTicks            uint64
}

type Engine struct {
	HealthAddress               string
	PollInterval                time.Duration
	LeaseDuration               time.Duration
	HeartbeatInterval           time.Duration
	ShutdownTimeout             time.Duration
	MaxAttempts                 int
	WorkerIDEnvironmentVariable string
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
		MaxTicks            uint64 `yaml:"maxTicks"`
	} `yaml:"api"`
	Engine struct {
		HealthAddress               string `yaml:"healthAddress"`
		PollInterval                string `yaml:"pollInterval"`
		LeaseDuration               string `yaml:"leaseDuration"`
		HeartbeatInterval           string `yaml:"heartbeatInterval"`
		ShutdownTimeout             string `yaml:"shutdownTimeout"`
		MaxAttempts                 int    `yaml:"maxAttempts"`
		WorkerIDEnvironmentVariable string `yaml:"workerIdEnvironmentVariable"`
	} `yaml:"engine"`
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
	return Config{HTTP: httpConfig, Database: databaseConfig, API: apiConfig}, nil
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
	return Config{Database: databaseConfig, Engine: engineConfig}, nil
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
	maxTicks := raw.API.MaxTicks
	if maxTicks == 0 {
		maxTicks = 10_000
	}
	if maxTicks > 10_000 {
		return API{}, fmt.Errorf("api.maxTicks must not exceed 10000")
	}
	return API{ScenarioCatalogPath: catalogPath, MaxTicks: maxTicks}, nil
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
	return Engine{
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
