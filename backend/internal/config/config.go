package config

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	HTTP     HTTPServer
	Database Database
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
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config file %q: %w", path, err)
	}
	defer file.Close()

	var raw fileConfig
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("decode config file %q: %w", path, err)
	}

	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("config file %q must contain one YAML document", path)
		}
		return Config{}, fmt.Errorf("decode trailing content in config file %q: %w", path, err)
	}

	httpConfig, err := parseHTTPConfig(raw)
	if err != nil {
		return Config{}, fmt.Errorf("validate config file %q: %w", path, err)
	}
	databaseConfig, err := parseDatabaseConfig(raw)
	if err != nil {
		return Config{}, fmt.Errorf("validate config file %q: %w", path, err)
	}

	return Config{HTTP: httpConfig, Database: databaseConfig}, nil
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
