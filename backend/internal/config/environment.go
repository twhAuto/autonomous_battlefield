// Names the environment variables the backend reads and validates the values every
// binary shares.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Environment variables with fixed names. Model profiles name their own credential,
// model and availability variables in YAML.
const (
	envRuntime               = "BATTLEFIELD_ENV"
	envDatabaseURL           = "DATABASE_URL"
	envResultCacheURL        = "BATTLEFIELD_REDIS_URL"
	envAuthMode              = "BATTLEFIELD_AUTH_MODE"
	envAuthEnabled           = "BATTLEFIELD_AUTH_ENABLED"
	envAdminUsername         = "BATTLEFIELD_ADMIN_USERNAME"
	envAdminPassword         = "BATTLEFIELD_ADMIN_PASSWORD"
	envSessionSecret         = "BATTLEFIELD_SESSION_SECRET"
	envCookieSecure          = "BATTLEFIELD_AUTH_COOKIE_SECURE"
	envEngineWorkerID        = "ENGINE_WORKER_ID"
	envAgentSecret           = "BATTLEFIELD_AGENT_SECRET"
	envCapabilitySecret      = "BATTLEFIELD_MCP_CAPABILITY_SECRET"
	envCLIIsolationVerified  = "BATTLEFIELD_CLI_ISOLATION_VERIFIED"
	minimumSecretBytes       = 32
	exampleSecretPlaceholder = "replace-with-"
)

// runtimeProduction validates BATTLEFIELD_ENV for every binary, so a typo such as
// "prod" fails startup instead of silently disabling production-only guards.
func runtimeProduction() (bool, error) {
	switch strings.TrimSpace(os.Getenv(envRuntime)) {
	case "", "dev":
		return false, nil
	case "production":
		return true, nil
	}
	return false, fmt.Errorf("environment variable %q must be dev or production", envRuntime)
}

// retiredPublicSecret was the built-in local capability secret. It is published in the
// repository history, so a key signed with it is forgeable.
const retiredPublicSecret = "local-development-capability-secret-change-me"

// requireSecret rejects short values, the placeholders copied from .env.example, and
// the retired public development secret.
func requireSecret(name, value string, minimum int) error {
	if len(value) < minimum || strings.HasPrefix(value, exampleSecretPlaceholder) || value == retiredPublicSecret {
		return fmt.Errorf("environment variable %q must contain an independent secret of at least %d characters", name, minimum)
	}
	return nil
}

// checkLegacyVariableKeys accepts the retired *EnvironmentVariable keys only when they
// name the fixed variable. Deployed ConfigMaps keep them until no rollback target
// requires them.
func checkLegacyVariableKeys(raw fileConfig) error {
	for _, key := range []struct{ field, value, fixed string }{
		{"database.urlEnvironmentVariable", raw.Database.URLEnvironmentVariable, envDatabaseURL},
		{"resultCache.urlEnvironmentVariable", raw.ResultCache.URLEnvironmentVariable, envResultCacheURL},
		{"auth.enabledEnvironmentVariable", raw.Auth.EnabledEnvironmentVariable, envAuthEnabled},
		{"auth.usernameEnvironmentVariable", raw.Auth.UsernameEnvironmentVariable, envAdminUsername},
		{"auth.passwordEnvironmentVariable", raw.Auth.PasswordEnvironmentVariable, envAdminPassword},
		{"auth.sessionSecretEnvironmentVariable", raw.Auth.SessionSecretEnvironmentVariable, envSessionSecret},
		{"auth.cookieSecureEnvironmentVariable", raw.Auth.CookieSecureEnvironmentVariable, envCookieSecure},
		{"engine.workerIdEnvironmentVariable", raw.Engine.WorkerIDEnvironmentVariable, envEngineWorkerID},
		{"ai.capabilitySecretEnvironmentVariable", raw.AI.CapabilitySecretEnvironmentVariable, envCapabilitySecret},
	} {
		if value := strings.TrimSpace(key.value); value != "" && value != key.fixed {
			return fmt.Errorf("%s is no longer configurable; remove it (the variable is always %s)", key.field, key.fixed)
		}
	}
	return nil
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
