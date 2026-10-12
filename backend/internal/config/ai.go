// Parses and validates AI configuration.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/generation"
)

type AI struct {
	BackgroundGeneration generation.Config
	Environment          string
	AgentConcurrency     int
	AgentHealthAddress   string
	AIGatewayAddress     string
	MCPURL               string
	DecisionTimeout      time.Duration
	AgentSecret          string
	CapabilitySecret     string
	CapabilitySecretEnv  string
	AllowedOrigins       []string
	Profiles             []ai.ModelProfile
}

func parseAIConfig(raw fileConfig) (AI, error) {
	environment, err := parseAIEnvironment(raw.AI.Environment)
	if err != nil {
		return AI{}, err
	}
	concurrency := 1
	if raw.AI.AgentConcurrency != nil {
		concurrency = *raw.AI.AgentConcurrency
	}
	if concurrency < 1 || concurrency > 4 {
		return AI{}, fmt.Errorf("ai.agentConcurrency must be between 1 and 4")
	}
	health := stringOrDefault(raw.AI.AgentHealthAddress, ":8083")
	gatewayAddress, err := aiGatewayAddress(raw.AI.AIGatewayAddress, raw.AI.LegacyMCPAddress)
	if err != nil {
		return AI{}, err
	}
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
	return AI{AgentConcurrency: concurrency, BackgroundGeneration: gen, Environment: environment, AgentHealthAddress: health, AIGatewayAddress: gatewayAddress, MCPURL: mcpURL,
		DecisionTimeout: timeout, CapabilitySecret: secret, CapabilitySecretEnv: secretEnv,
		AllowedOrigins: append([]string(nil), raw.AI.AllowedOrigins...), Profiles: append([]ai.ModelProfile(nil), raw.AI.Profiles...)}, nil
}

// aiGatewayAddress accepts the legacy mcpAddress key so ConfigMaps can switch keys
// only after every rollback target understands aiGatewayAddress.
func aiGatewayAddress(current, legacy string) (string, error) {
	current, legacy = strings.TrimSpace(current), strings.TrimSpace(legacy)
	if current != "" && legacy != "" && current != legacy {
		return "", fmt.Errorf("ai.aiGatewayAddress and legacy ai.mcpAddress disagree; set only ai.aiGatewayAddress")
	}
	return stringOrDefault(current, stringOrDefault(legacy, ":8084")), nil
}

func parseAIEnvironment(value string) (string, error) {
	environment := stringOrDefault(value, "local")
	if environment != "local" && environment != "cluster" {
		return "", fmt.Errorf("ai.environment must be local or cluster")
	}
	return environment, nil
}

// localDevelopmentCapabilitySecret lets local processes that never mint or verify
// capabilities parse configuration. It is public, so LoadAIGateway rejects it.
const localDevelopmentCapabilitySecret = "local-development-capability-secret-change-me"

func aiCapabilitySecret(configuredEnvironmentVariable, environment string) (string, string) {
	secretEnv := stringOrDefault(configuredEnvironmentVariable, "BATTLEFIELD_MCP_CAPABILITY_SECRET")
	secret := strings.TrimSpace(os.Getenv(secretEnv))
	if secret == "" && environment == "local" {
		secret = localDevelopmentCapabilitySecret
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

func validateProfileRoles(profile ai.ModelProfile) error {
	seen := map[string]bool{}
	for _, role := range profile.AllowedRoles {
		if seen[role] || (role != "player" && role != "developer" && role != "admin") {
			return fmt.Errorf("invalid allowedRoles for profile %q", profile.ID)
		}
		seen[role] = true
	}
	return nil
}

func validateAIProfile(profile ai.ModelProfile) error {
	if err := validateProfileRoles(profile); err != nil {
		return err
	}
	if os.Getenv("BATTLEFIELD_ENV") == "production" && profile.Enabled && profile.Runtime == "cli" && (profile.AvailabilityEnvironmentVariable == "" || strings.EqualFold(os.Getenv(profile.AvailabilityEnvironmentVariable), "true")) && os.Getenv("BATTLEFIELD_CLI_ISOLATION_VERIFIED") != "true" {
		return fmt.Errorf("CLI profiles require verified VM isolation in production")
	}

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
