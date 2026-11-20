// Parses and validates engine configuration.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
)

type Engine struct {
	KnowledgeRetrieval ai.KnowledgeLimits
	HealthAddress      string
	PollInterval       time.Duration
	LeaseDuration      time.Duration
	HeartbeatInterval  time.Duration
	ShutdownTimeout    time.Duration
	MaxAttempts        int
	// Concurrency is how many battles the engine runs at once, one claim loop each.
	Concurrency int
	// WorkerID names this engine in leases; it defaults to the hostname and process ID.
	WorkerID string
}

// defaultEngineConcurrency applies when the file omits engine.concurrency. Deployed
// ConfigMaps leave the key out so a rollback to an image that predates it still starts.
const defaultEngineConcurrency = 4

func parseEngineConfig(raw fileConfig) (Engine, error) {
	healthAddress := strings.TrimSpace(raw.Engine.HealthAddress)
	if healthAddress == "" {
		healthAddress = ":8081"
	}
	pollInterval, err := parseOptionalDuration("engine.pollInterval", raw.Engine.PollInterval, time.Second)
	if err != nil {
		return Engine{}, err
	}
	leaseDuration, err := parseOptionalDuration("engine.leaseDuration", raw.Engine.LeaseDuration, 30*time.Second)
	if err != nil {
		return Engine{}, err
	}
	heartbeatInterval, err := parseOptionalDuration("engine.heartbeatInterval", raw.Engine.HeartbeatInterval, 10*time.Second)
	if err != nil {
		return Engine{}, err
	}
	shutdownTimeout, err := parseOptionalDuration("engine.shutdownTimeout", raw.Engine.ShutdownTimeout, 10*time.Second)
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
	concurrency, err := parseEngineConcurrency(raw)
	if err != nil {
		return Engine{}, err
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
		MaxAttempts: maxAttempts, Concurrency: concurrency, WorkerID: workerID(),
	}, nil
}

// parseEngineConcurrency reads engine.concurrency. The API reads it too, to know how many
// league battles the engine can run at once.
func parseEngineConcurrency(raw fileConfig) (int, error) {
	concurrency := defaultEngineConcurrency
	if raw.Engine.Concurrency != nil {
		concurrency = *raw.Engine.Concurrency
	}
	if concurrency < 1 || concurrency > 8 {
		return 0, fmt.Errorf("engine.concurrency must be between 1 and 8")
	}
	return concurrency, nil
}

func workerID() string {
	if id := strings.TrimSpace(os.Getenv(envEngineWorkerID)); id != "" {
		return id
	}
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "engine"
	}
	return hostname + "-" + strconv.Itoa(os.Getpid())
}
