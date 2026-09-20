// Parses and validates database configuration.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Database struct {
	URL                   string
	ConnectTimeout        time.Duration
	MaxConnections        int32
	MinConnections        int32
	MaxConnectionLifetime time.Duration
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
