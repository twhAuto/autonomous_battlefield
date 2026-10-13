// Checks provider-to-binary mapping and that the shared login check dispatches per provider.

package cliisolation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBinary(t *testing.T) {
	for provider, want := range map[string]string{ProviderCodex: "codex", ProviderClaude: "claude", ProviderAntigravity: "agy", "openai": ""} {
		if got := Binary(provider); got != want {
			t.Fatalf("Binary(%q)=%q, want %q", provider, got, want)
		}
	}
}

func TestCheckAuthDispatchesByProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	if err := CheckAuth(context.Background(), ProviderCodex); err == nil {
		t.Fatal("missing Codex login accepted")
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckAuth(context.Background(), ProviderCodex); err != nil {
		t.Fatal(err)
	}
	// Neither CLI is on PATH, so both checks must fail rather than pass by default.
	for _, provider := range []string{ProviderClaude, ProviderAntigravity, "openai"} {
		if err := CheckAuth(context.Background(), provider); err == nil {
			t.Fatalf("%s login accepted without a CLI", provider)
		}
	}
}
