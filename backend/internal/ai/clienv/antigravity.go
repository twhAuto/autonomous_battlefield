// Package clienv prepares isolated subscription CLI sessions without importing
// personal configuration, API keys, or unrelated MCP servers.
package clienv

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const geminiDirectory = ".gemini"

// PrepareAntigravity uses the OS keyring or a private copy of the CLI-native
// fallback login. HOME and XDG configuration paths remain private per invocation.
func PrepareAntigravity(home, mcpURL, capability string, toolNames ...string) ([]string, error) {
	if len(toolNames) == 0 {
		toolNames = []string{"get_decision_context", "submit_strategy_plan"}
	}
	allowed := []string{}
	for _, name := range toolNames {
		allowed = append(allowed, "mcp(battlefield/"+name+")")
	}
	// Do not set toolPermission=strict: agy 1.1.27 denies even explicitly
	// allowed MCP calls in that mode. Enforce the explicit action rules below.
	settings := map[string]any{
		"allowNonWorkspaceAccess": false,
		"enableTelemetry":         false,
		"useG1Credits":            false,
		"permissions": map[string]any{
			"allow": allowed,
			"deny":  []string{"read_file(*)", "write_file(*)", "command(*)", "unsandboxed(*)", "read_url(*)", "execute_url(*)"},
		},
	}
	if err := writeJSON(filepath.Join(home, geminiDirectory, "antigravity-cli", "settings.json"), settings); err != nil {
		return nil, err
	}
	if err := copyAntigravityLogin(home); err != nil {
		return nil, err
	}
	servers := map[string]any{}
	if mcpURL != "" {
		servers["battlefield"] = map[string]any{
			"serverUrl": mcpURL,
			"headers":   map[string]string{"Authorization": "Bearer " + capability},
		}
	}
	if err := writeJSON(filepath.Join(home, geminiDirectory, "config", "mcp_config.json"), map[string]any{"mcpServers": servers}); err != nil {
		return nil, err
	}
	// Only transport variables required for the local keyring and network proxy
	// survive. In particular, no provider keys, CLI overrides, or agent secrets do.
	environment := []string{}
	for _, key := range []string{"PATH", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(key); ok {
			environment = append(environment, key+"="+value)
		}
	}
	environment = append(environment, "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "XDG_CACHE_HOME="+filepath.Join(home, ".cache"), "XDG_DATA_HOME="+filepath.Join(home, ".local", "share"), "TMPDIR="+home, "AGY_CLI_DISABLE_AUTO_UPDATE=1")
	return environment, nil
}

// Linux headless sessions can use the CLI's native file-backed token storage.
// Copy only that credential, never settings, extensions, or personal MCP servers.
// The caller removes the entire private home after the invocation.
func copyAntigravityLogin(home string) error {
	sourceHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate Antigravity login: %w", err)
	}
	relative := filepath.Join(geminiDirectory, "antigravity-cli", "antigravity-oauth-token")
	source := filepath.Join(sourceHome, relative)
	info, err := os.Lstat(source)
	if os.IsNotExist(err) {
		return nil
	} // Native keyring may supply the login.
	if err != nil {
		return fmt.Errorf("inspect Antigravity login: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 65536 {
		return fmt.Errorf("Antigravity login must be a regular token file under 64 KiB")
	}
	token, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read Antigravity login: %w", err)
	}
	return os.WriteFile(filepath.Join(home, relative), token, 0o600)
}

func writeJSON(path string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o600)
}

// CheckAntigravityAuth asks for account usage, not model inference. A cached
// host login must work with the same isolation as a real decision session.
func CheckAntigravityAuth(ctx context.Context) error {
	temporary, err := os.MkdirTemp("", "battlefield-agy-auth-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	environment, err := PrepareAntigravity(temporary, "", "")
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "agy", "--print", "/usage", "--output-format", "json")
	command.Env = environment
	command.Dir = temporary
	// Do not inherit stdin or expose account details in availability errors.
	if err := command.Run(); err != nil {
		return fmt.Errorf("Antigravity login unavailable: run agy interactively on this host, then retry")
	}
	return nil
}
