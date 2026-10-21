// Parses and validates the AI sections: the model catalog, the agent and the gateway.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

// Models is the model-profile catalog the API server lists and the agent runs.
type Models struct {
	// Environment is local or cluster.
	Environment     string
	DecisionTimeout time.Duration
	Profiles        []ai.ModelProfile
}

type Agent struct {
	Concurrency   int
	HealthAddress string
	MCPURL        string
	Secret        string
}

type Gateway struct {
	Address          string
	AllowedOrigins   []string
	CapabilitySecret string
	AgentSecret      string
}

func parseModels(raw fileConfig, production bool) (Models, error) {
	environment, err := parseAIEnvironment(raw.AI.Environment)
	if err != nil {
		return Models{}, err
	}
	timeout, err := parseDecisionTimeout(raw)
	if err != nil {
		return Models{}, err
	}
	if err := validateAIProfiles(raw.AI.Profiles, production); err != nil {
		return Models{}, err
	}
	return Models{Environment: environment, DecisionTimeout: timeout, Profiles: append([]ai.ModelProfile(nil), raw.AI.Profiles...)}, nil
}

func parseDecisionTimeout(raw fileConfig) (time.Duration, error) {
	return parseOptionalDuration("ai.decisionTimeout", raw.AI.DecisionTimeout, 2*time.Minute)
}

func parseAgent(raw fileConfig) (Agent, error) {
	concurrency := 1
	if raw.AI.AgentConcurrency != nil {
		concurrency = *raw.AI.AgentConcurrency
	}
	if concurrency < 1 || concurrency > 4 {
		return Agent{}, fmt.Errorf("ai.agentConcurrency must be between 1 and 4")
	}
	secret := os.Getenv(envAgentSecret)
	if err := requireSecret(envAgentSecret, secret, minimumSecretBytes); err != nil {
		return Agent{}, err
	}
	return Agent{
		Concurrency:   concurrency,
		HealthAddress: stringOrDefault(raw.AI.AgentHealthAddress, ":8083"),
		MCPURL:        stringOrDefault(raw.AI.MCPURL, "http://127.0.0.1:8084/mcp"),
		Secret:        secret,
	}, nil
}

func parseGateway(raw fileConfig) (Gateway, error) {
	address, err := aiGatewayAddress(raw.AI.AIGatewayAddress, raw.AI.LegacyMCPAddress)
	if err != nil {
		return Gateway{}, err
	}
	capabilitySecret := strings.TrimSpace(os.Getenv(envCapabilitySecret))
	if err := requireSecret(envCapabilitySecret, capabilitySecret, minimumSecretBytes); err != nil {
		return Gateway{}, err
	}
	agentSecret := os.Getenv(envAgentSecret)
	if err := requireSecret(envAgentSecret, agentSecret, minimumSecretBytes); err != nil {
		return Gateway{}, err
	}
	if agentSecret == capabilitySecret {
		return Gateway{}, fmt.Errorf("environment variables %q and %q must hold different secrets", envAgentSecret, envCapabilitySecret)
	}
	return Gateway{
		Address:          address,
		AllowedOrigins:   append([]string(nil), raw.AI.AllowedOrigins...),
		CapabilitySecret: capabilitySecret,
		AgentSecret:      agentSecret,
	}, nil
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

func validateAIProfiles(profiles []ai.ModelProfile, production bool) error {
	seen := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		id := strings.TrimSpace(profile.ID)
		if id == "" || seen[id] {
			return fmt.Errorf("ai profile IDs must be unique and non-empty")
		}
		seen[id] = true
		if err := validateAIProfile(profile, production); err != nil {
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

func validateAIProfile(profile ai.ModelProfile, production bool) error {
	if err := ai.ValidateModelOptions(profile); err != nil {
		return err
	}
	if err := validateProfileRoles(profile); err != nil {
		return err
	}
	if production && cliProfileMayRun(profile) && os.Getenv(envCLIIsolationVerified) != "true" {
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

// cliProfileMayRun reports whether an enabled CLI profile could be offered. A profile
// without an availability variable is always offered; otherwise the variable decides,
// read exactly as the profile catalog reads it.
func cliProfileMayRun(profile ai.ModelProfile) bool {
	if !profile.Enabled || profile.Runtime != "cli" {
		return false
	}
	return profile.AvailabilityEnvironmentVariable == "" || profile.AvailabilityFlagSet()
}
