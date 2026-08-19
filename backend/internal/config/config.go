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
	HTTP HTTPServer
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

	return Config{HTTP: httpConfig}, nil
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
