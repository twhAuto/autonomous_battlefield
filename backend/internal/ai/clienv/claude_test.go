package clienv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeAuth(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		exit         int
		ok           bool
	}{
		{"subscription", `{"loggedIn":true,"authMethod":"claude.ai"}`, 0, true},
		{"logged out", `{"loggedIn":false,"authMethod":"claude.ai"}`, 0, false},
		{"api key", `{"loggedIn":true,"authMethod":"api_key"}`, 0, false},
		{"invalid", "invalid", 0, false},
		{"failed", "", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			t.Setenv("PATH", bin)
			script := "#!/bin/sh\n[ \"$1 $2\" = 'auth status' ] || exit 2\nprintf '%s' '" + tc.output + "'\n"
			if tc.exit != 0 {
				script += "exit 1\n"
			}
			if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := CheckClaudeAuth(context.Background()); (err == nil) != tc.ok {
				t.Fatalf("auth: %v", err)
			}
		})
	}
}
func TestClaudeEnvironment(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CONFIG_DIR", "BATTLEFIELD_AGENT_SECRET"} {
		t.Setenv(key, "secret-value")
	}
	t.Setenv("HOME", "/login-home")
	env := strings.Join(ClaudeEnvironment(), "\n")
	if strings.Contains(env, "secret-value") || !strings.Contains(env, "HOME=/login-home") || !strings.Contains(env, "DISABLE_AUTOUPDATER=1") {
		t.Fatal(env)
	}
}
