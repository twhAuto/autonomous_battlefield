// Parses and validates engine configuration.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

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
