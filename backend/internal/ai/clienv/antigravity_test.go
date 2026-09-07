// Checks isolated Antigravity login preparation, tool restrictions, filesystem failures,
// and non-generating authentication probes.

package clienv

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareAntigravityRestrictsToolsAndEnvironment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home := t.TempDir()
	t.Setenv("GEMINI_API_KEY", "must-not-inherit")
	t.Setenv("BATTLEFIELD_MCP_CAPABILITY_SECRET", "root-secret")
	t.Setenv("ANTIGRAVITY_LS_ADDRESS", "personal-session")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/keyring")
	env, err := PrepareAntigravity(home, `http://localhost/mcp?quoted="yes"`, `quoted-"-token`)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	for _, secret := range []string{"must-not-inherit", "root-secret", "personal-session", "quoted-"} {
		if strings.Contains(joined, secret) {
			t.Fatal("ambient credential or session leaked")
		}
	}
	if !strings.Contains(joined, "HOME="+home) || !strings.Contains(joined, "DBUS_SESSION_BUS_ADDRESS=unix:path=/keyring") {
		t.Fatal("isolated home/keyring transport missing")
	}
	assertPrivateAntigravitySettings(t, home)
	assertAntigravityMCPToken(t, home)
}

func assertPrivateAntigravitySettings(t *testing.T, home string) {
	t.Helper()
	for _, file := range []string{".gemini/antigravity-cli/settings.json", ".gemini/config/mcp_config.json"} {
		info, err := os.Stat(filepath.Join(home, file))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatal("configuration is not private")
		}
	}
	data, _ := os.ReadFile(filepath.Join(home, ".gemini/antigravity-cli/settings.json"))
	var settings struct {
		ToolPermission string                         `json:"toolPermission"`
		Permissions    struct{ Allow, Deny []string } `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.ToolPermission != "" || strings.Join(settings.Permissions.Allow, ",") != "mcp(battlefield/get_decision_context),mcp(battlefield/submit_strategy_plan)" {
		t.Fatal("MCP allowlist widened")
	}
	deny := strings.Join(settings.Permissions.Deny, ",")
	for _, rule := range []string{"command(*)", "read_file(*)", "write_file(*)", "unsandboxed(*)", "read_url(*)", "execute_url(*)"} {
		if !strings.Contains(deny, rule) {
			t.Fatalf("missing %s", rule)
		}
	}
}

func assertAntigravityMCPToken(t *testing.T, home string) {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(home, ".gemini/config/mcp_config.json"))
	var mcp struct {
		Servers map[string]struct {
			URL     string            `json:"serverUrl"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &mcp); err != nil {
		t.Fatal(err)
	}
	if len(mcp.Servers) != 1 || mcp.Servers["battlefield"].Headers["Authorization"] != `Bearer quoted-"-token` {
		t.Fatal("MCP token escaping failed")
	}
}

func TestPrepareAntigravityFailsForUnwritablePaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareAntigravity(file, "", ""); err == nil {
		t.Fatal("invalid home accepted")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gemini", "config"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareAntigravity(home, "", ""); err == nil {
		t.Fatal("invalid MCP config directory accepted")
	}
}

func TestAntigravityAuthProbeIsIsolatedAndNonGenerating(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	script := `#!/bin/sh
[ "$1" = --print ] && [ "$2" = /usage ] && [ "$3" = --output-format ] && [ "$4" = json ] || exit 2
[ -f "$HOME/.gemini/antigravity-cli/settings.json" ] || exit 3
[ -z "${GEMINI_API_KEY:-}" ] || exit 4
exit 0
`
	path := filepath.Join(bin, "agy")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GEMINI_API_KEY", "do-not-charge")
	if err := CheckAntigravityAuth(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CheckAntigravityAuth(context.Background()); err == nil {
		t.Fatal("failed login accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CheckAntigravityAuth(ctx); err == nil {
		t.Fatal("cancelled probe accepted")
	}
}

func TestAntigravityCopiesOnlyNativeLogin(t *testing.T) {
	source := t.TempDir()
	t.Setenv("HOME", source)
	dir := filepath.Join(source, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(dir, "antigravity-oauth-token")
	if err := os.WriteFile(tokenPath, []byte("fixture-login"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"personal":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, err := PrepareAntigravity(destination, "", ""); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(destination, ".gemini", "antigravity-cli", "antigravity-oauth-token")
	data, err := os.ReadFile(copied)
	if err != nil || string(data) != "fixture-login" {
		t.Fatal("native login not copied")
	}
	info, err := os.Stat(copied)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("login copy must be private")
	}
	if err := os.WriteFile(copied, []byte("refreshed"), 0600); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(tokenPath)
	if string(original) != "fixture-login" {
		t.Fatal("isolated session changed host login")
	}
	settings, _ := os.ReadFile(filepath.Join(destination, ".gemini", "antigravity-cli", "settings.json"))
	if strings.Contains(string(settings), "personal") {
		t.Fatal("personal settings imported")
	}
	if err := os.Remove(tokenPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(copied, tokenPath); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareAntigravity(t.TempDir(), "", ""); err == nil {
		t.Fatal("symlink token accepted")
	}
}
