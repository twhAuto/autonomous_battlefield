package cliisolation

import (
	"fmt"
	"os"
	"path/filepath"
)

// CodexEnvironment preserves the native login location, but excludes API keys,
// provider overrides, and application secrets. The runner's --ignore-user-config
// and explicit feature flags keep personal configuration from supplying tools.
func CodexEnvironment() []string {
	return inheritEnvironment(loginEnvironmentKeys)
}

// CheckCodexAuth checks for the saved subscription login without model inference.
func CheckCodexAuth() error {
	home, err := os.UserHomeDir()
	if err == nil {
		_, err = os.Stat(filepath.Join(home, ".codex", "auth.json"))
	}
	if err != nil {
		return fmt.Errorf("codex CLI is not authenticated")
	}
	return nil
}
