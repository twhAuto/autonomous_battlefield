package config

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"testing"
)

func TestProductionAccessConfiguration(t *testing.T) {
	t.Setenv("BATTLEFIELD_ENV", "production")
	t.Setenv("BATTLEFIELD_AUTH_MODE", "cloudflare")
	t.Setenv("BATTLEFIELD_AUTH_ENABLED", "")
	t.Setenv("BATTLEFIELD_ACCESS_ISSUER", "https://friends.cloudflareaccess.com")
	t.Setenv("BATTLEFIELD_ACCESS_AUDIENCE", "application-audience")
	t.Setenv("BATTLEFIELD_PUBLIC_ORIGIN", "https://battle.example.com")
	t.Setenv("BATTLEFIELD_ACCESS_ROLES", `{"admin":["owner@example.com"],"player":["friend@example.com"]}`)
	t.Setenv("BATTLEFIELD_ACCESS_CI_CLIENT_ID", "0123456789abcdef0123456789abcdef.access")
	cfg, e := parseAuthConfig(fileConfig{})
	if e != nil || !cfg.Enabled || cfg.SessionSecret != "" || cfg.CIClientID != "0123456789abcdef0123456789abcdef.access" {
		t.Fatal(cfg, e)
	}
	for _, tt := range []struct{ key, value string }{
		{"BATTLEFIELD_AUTH_MODE", ""}, {"BATTLEFIELD_AUTH_MODE", "disabled-development"}, {"BATTLEFIELD_AUTH_MODE", "local-password"},
		{"BATTLEFIELD_AUTH_ENABLED", "false"}, {"BATTLEFIELD_ACCESS_ISSUER", "http://friends.cloudflareaccess.com"},
		{"BATTLEFIELD_ACCESS_ISSUER", "https://attacker.example.com"}, {"BATTLEFIELD_ACCESS_ISSUER", "https://friends.cloudflareaccess.com/path"},
		{"BATTLEFIELD_ACCESS_AUDIENCE", ""}, {"BATTLEFIELD_PUBLIC_ORIGIN", "http://battle.example.com"},
		{"BATTLEFIELD_ACCESS_CI_CLIENT_ID", "admin@example.com"},
		{"BATTLEFIELD_ACCESS_ROLES", `{}`}, {"BATTLEFIELD_ACCESS_ROLES", `{"admin":["A@example.com"],"player":["a@example.com"]}`},
		{"BATTLEFIELD_ACCESS_ROLES", `{"root":["a@example.com"]}`},
	} {
		t.Run(tt.key+tt.value, func(t *testing.T) {
			t.Setenv(tt.key, tt.value)
			if _, e := parseAuthConfig(fileConfig{}); e == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
}
func TestExplicitDevelopmentMode(t *testing.T) {
	t.Setenv("BATTLEFIELD_ENV", "dev")
	t.Setenv("BATTLEFIELD_AUTH_MODE", "")
	t.Setenv("BATTLEFIELD_AUTH_ENABLED", "")
	if _, e := parseAuthConfig(fileConfig{}); e == nil {
		t.Fatal("implicit anonymous access")
	}
	t.Setenv("BATTLEFIELD_AUTH_MODE", "disabled-development")
	if cfg, e := parseAuthConfig(fileConfig{}); e != nil || cfg.Enabled {
		t.Fatal(cfg, e)
	}
	t.Setenv("BATTLEFIELD_AUTH_ENABLED", "true")
	if _, e := parseAuthConfig(fileConfig{}); e == nil {
		t.Fatal("conflicting settings")
	}
}
func TestCLIProductionGateAndProfileRoles(t *testing.T) {
	t.Setenv("BATTLEFIELD_ENV", "production")
	t.Setenv("BATTLEFIELD_CLI_ISOLATION_VERIFIED", "")
	p := ai.ModelProfile{ID: "cli", Runtime: "cli", Enabled: true, MaxSteps: 3, Model: "pinned", SupportedVersionPrefix: "1."}
	if e := validateAIProfile(p); e == nil {
		t.Fatal("unverified CLI enabled")
	}
	t.Setenv("BATTLEFIELD_CLI_ISOLATION_VERIFIED", "true")
	if e := validateAIProfile(p); e != nil {
		t.Fatal(e)
	}
	p.AllowedRoles = []string{"player"}
	if e := validateAIProfile(p); e != nil {
		t.Fatal("explicit player CLI access rejected", e)
	}
	for _, roles := range [][]string{{"root"}, {"admin", "admin"}} {
		p.AllowedRoles = roles
		if e := validateAIProfile(p); e == nil {
			t.Fatal(roles)
		}
	}
}
