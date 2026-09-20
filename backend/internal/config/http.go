// Parses and validates HTTP configuration.
package config

import (
	"fmt"
	"strings"
	"time"
)

type HTTPServer struct {
	Address           string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
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
