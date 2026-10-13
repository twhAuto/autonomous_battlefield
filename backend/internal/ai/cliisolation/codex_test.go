package cliisolation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := CheckCodexAuth(); err == nil {
		t.Fatal("missing login accepted")
	}
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckCodexAuth(); err != nil {
		t.Fatal(err)
	}
}

func TestCodexEnvironment(t *testing.T) {
	for _, key := range []string{"OPENAI_API_KEY", "CODEX_HOME", "BATTLEFIELD_AGENT_SECRET"} {
		t.Setenv(key, "secret-value")
	}
	t.Setenv("HOME", "/login-home")
	env := strings.Join(CodexEnvironment(), "\n")
	if strings.Contains(env, "secret-value") || !strings.Contains(env, "HOME=/login-home") {
		t.Fatal(env)
	}
}
