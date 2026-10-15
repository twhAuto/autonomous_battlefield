package cliisolation

import (
	"context"
	"fmt"
)

// Subscription CLI provider identifiers, as configured on model profiles.
const (
	ProviderCodex       = "codex-cli"
	ProviderClaude      = "claude-cli"
	ProviderAntigravity = "antigravity-cli"
)

var binaries = map[string]string{ProviderCodex: "codex", ProviderClaude: "claude", ProviderAntigravity: "agy"}

// Binary returns the executable for a CLI provider, or "" when the provider is unsupported.
func Binary(provider string) string {
	return binaries[provider]
}

// CheckAuth checks the provider's saved subscription login without model inference.
func CheckAuth(ctx context.Context, provider string) error {
	switch provider {
	case ProviderCodex:
		return CheckCodexAuth()
	case ProviderClaude:
		return CheckClaudeAuth(ctx)
	case ProviderAntigravity:
		return CheckAntigravityAuth(ctx)
	}
	return fmt.Errorf("unsupported CLI provider %q", provider)
}
