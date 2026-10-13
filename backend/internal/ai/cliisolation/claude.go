package cliisolation

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// ClaudeEnvironment preserves the native login location, but excludes API keys,
// provider overrides, and application secrets. Restricted mode and explicit CLI
// settings prevent personal configuration from supplying tools or hooks.
func ClaudeEnvironment() []string {
	return append(inheritEnvironment(loginEnvironmentKeys), "DISABLE_AUTOUPDATER=1")
}

// CheckClaudeAuth checks the saved subscription login without model inference.
func CheckClaudeAuth(ctx context.Context) error {
	command := exec.CommandContext(ctx, "claude", "auth", "status", "--json")
	command.Env = ClaudeEnvironment()
	var status struct {
		LoggedIn   bool   `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
	}
	output, err := command.Output()
	if err != nil || json.Unmarshal(output, &status) != nil || !status.LoggedIn || status.AuthMethod != "claude.ai" {
		return fmt.Errorf("Claude subscription login unavailable: run claude auth login on this host")
	}
	return nil
}
