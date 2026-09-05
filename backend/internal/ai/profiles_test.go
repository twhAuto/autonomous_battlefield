package ai

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProfileRegistryEvaluatesAllowlist(t *testing.T) {
	t.Setenv("TEST_AI_KEY", "key")
	t.Setenv("TEST_AI_MODEL", "model-x")
	profiles := []ModelProfile{
		{ID: "api", DisplayName: "API", Provider: "openai", Runtime: "api", Scope: "cluster", CredentialEnvironmentVariable: "TEST_AI_KEY", ModelEnvironmentVariable: "TEST_AI_MODEL", Enabled: true, MaxSteps: 3, MaxOutputTokens: 100},
		{ID: "disabled", DisplayName: "Off", Provider: "openai", Runtime: "api", Scope: "cluster"},
		{ID: "local", DisplayName: "CLI", Provider: "codex-cli", Runtime: "cli", Scope: "local", Enabled: true, MaxSteps: 3},
		{ID: "invalid", DisplayName: "Bad", Provider: "x", Runtime: "unknown", Scope: "cluster", Enabled: true},
	}
	registry := NewProfileRegistry("cluster", profiles)
	profile, err := registry.Require("api")
	if err != nil || profile.ModelLabel != "model-x" || !profile.ToolCapability {
		t.Fatalf("profile=%#v err=%v", profile, err)
	}
	if ids := registry.AvailableIDs(); len(ids) != 1 || ids[0] != "api" {
		t.Fatalf("available IDs=%v", ids)
	}
	for _, id := range []string{"disabled", "local", "invalid", "missing"} {
		if _, err := registry.Require(id); err == nil {
			t.Fatalf("profile %s available", id)
		}
	}
	if got := registry.List(); len(got) != 4 || got[0].ID != "api" {
		t.Fatalf("profiles=%#v", got)
	}
}

func TestLocalAPIProfileRequiresCredentials(t *testing.T) {
	t.Setenv("MISSING_KEY", "")
	t.Setenv("MISSING_MODEL", "")
	registry := NewProfileRegistry("local", []ModelProfile{{ID: "api", DisplayName: "API", Provider: "openai", Runtime: "api", Scope: "cluster", CredentialEnvironmentVariable: "MISSING_KEY", ModelEnvironmentVariable: "MISSING_MODEL", Enabled: true, MaxSteps: 1, MaxOutputTokens: 1}})
	profile, _ := registry.Get("api")
	if profile.Available || profile.UnavailableReason == "" {
		t.Fatalf("profile=%#v", profile)
	}
}

func TestClusterAPIProfileUsesSafeAvailabilityFlag(t *testing.T) {
	t.Setenv("TEST_PROFILE_AVAILABLE", "false")
	t.Setenv("TEST_CLUSTER_MODEL", "model-x")
	base := ModelProfile{ID: "api", DisplayName: "API", Provider: "openai", Runtime: "api", Scope: "cluster", ModelEnvironmentVariable: "TEST_CLUSTER_MODEL", CredentialEnvironmentVariable: "TEST_CLUSTER_KEY", AvailabilityEnvironmentVariable: "TEST_PROFILE_AVAILABLE", Enabled: true, MaxSteps: 1, MaxOutputTokens: 1}
	profile, _ := NewProfileRegistry("cluster", []ModelProfile{base}).Get(base.ID)
	if profile.Available || profile.UnavailableReason == "" {
		t.Fatalf("profile=%#v", profile)
	}
	t.Setenv("TEST_PROFILE_AVAILABLE", "true")
	profile, err := NewProfileRegistry("cluster", []ModelProfile{base}).Require(base.ID)
	if err != nil || profile.ModelLabel != "model-x" {
		t.Fatalf("profile=%#v err=%v", profile, err)
	}
}

func TestClusterCatalogCanAdvertiseExternalCLIWithoutMakingItExecutable(t *testing.T) {
	t.Setenv("TEST_CODEX_HOST_AVAILABLE", "true")
	base := ModelProfile{ID: "codex-host", DisplayName: "Codex host", Provider: "codex-cli", Runtime: "cli", Model: "gpt-5.6-luna", Scope: "local", AvailabilityEnvironmentVariable: "TEST_CODEX_HOST_AVAILABLE", SupportedVersionPrefix: "codex-cli 0.151.", Enabled: true, MaxSteps: 3}
	catalogProfile, err := NewProfileCatalog("cluster", []ModelProfile{base}).Require(base.ID)
	if err != nil || catalogProfile.ModelLabel != "gpt-5.6-luna" {
		t.Fatalf("catalog profile=%#v err=%v", catalogProfile, err)
	}
	executorProfile, _ := NewProfileRegistry("cluster", []ModelProfile{base}).Get(base.ID)
	if executorProfile.Available || executorProfile.UnavailableReason != "host CLI agent is not connected" {
		t.Fatalf("executor profile=%#v", executorProfile)
	}
	t.Setenv("TEST_CODEX_HOST_AVAILABLE", "false")
	catalogProfile, _ = NewProfileCatalog("cluster", []ModelProfile{base}).Get(base.ID)
	if catalogProfile.Available || catalogProfile.ModelLabel != "gpt-5.6-luna" {
		t.Fatalf("disabled external profile=%#v", catalogProfile)
	}
}

func TestLocalCLIProfileRequiresPinnedVersionAndAuthentication(t *testing.T) {
	bin := t.TempDir()
	home := t.TempDir()
	t.Setenv("PATH", bin)
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho 'codex-cli 0.151.2'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := ModelProfile{ID: "codex", DisplayName: "Codex", Provider: "codex-cli", Runtime: "cli", Model: "gpt-5.6-luna", Scope: "local", SupportedVersionPrefix: "codex-cli 0.151.", Enabled: true, MaxSteps: 3}
	profile, err := NewProfileRegistry("local", []ModelProfile{base}).Require(base.ID)
	if err != nil || profile.ModelLabel != "gpt-5.6-luna" {
		t.Fatalf("profile=%#v err=%v", profile, err)
	}
	base.SupportedVersionPrefix = "codex-cli 0.150."
	profile, _ = NewProfileRegistry("local", []ModelProfile{base}).Get(base.ID)
	if profile.Available || profile.UnavailableReason != "codex CLI version is not supported" {
		t.Fatalf("profile=%#v", profile)
	}
}

func TestAntigravityProfileRequiresWorkingIsolatedLogin(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	binary := filepath.Join(bin, "agy")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 1.1.27; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	profile := ModelProfile{ID: "gemini-subscription-local", DisplayName: "Antigravity", Provider: "antigravity-cli", Runtime: "cli", Scope: "local", SupportedVersionPrefix: "1.1.", Enabled: true}
	got, _ := NewProfileRegistry("local", []ModelProfile{profile}).Get(profile.ID)
	if got.Available {
		t.Fatal("installed but unauthenticated CLI advertised")
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 1.1.27; fi\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewProfileRegistry("local", []ModelProfile{profile}).Require(profile.ID); err != nil {
		t.Fatal(err)
	}
	profile.Provider = "gemini-cli"
	if _, err := NewProfileRegistry("local", []ModelProfile{profile}).Require(profile.ID); err == nil {
		t.Fatal("retired CLI still advertised")
	}
}
