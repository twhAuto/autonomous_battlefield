package profile

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
)

func TestAccessPolicyControlsSelectionsWithoutChangingConfiguration(t *testing.T) {
	base := NewCatalog("cluster", []ai.ModelProfile{{ID: "api", DisplayName: "API", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true}})
	policy := AccessPolicy{Providers: map[string]bool{"openai": true}, Profiles: map[string]Access{"api": {Enabled: true, AllowedRoles: []string{"player"}}}}
	var failure error
	registry := base.WithAccessPolicy(func() (AccessPolicy, error) { return policy, failure })
	ctx := authn.WithActor(context.Background(), authn.Actor{ID: "friend", Role: authn.Player})
	p, err := registry.Require("api")
	if err != nil || !Allowed(ctx, p) {
		t.Fatalf("approved profile: %+v %v", p, err)
	}
	policy.Providers["openai"] = false
	if _, err = registry.Require("api"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("disabled provider accepted")
	}
	if registry.List()[0].Available {
		t.Fatal("catalog still available")
	}
	policy.Providers["openai"] = true
	policy.Profiles["api"] = Access{Enabled: false, AllowedRoles: []string{"player"}}
	if _, err = registry.Require("api"); err == nil {
		t.Fatal("disabled profile accepted")
	}
	policy.Profiles["api"] = Access{Enabled: true, AllowedRoles: []string{}}
	p, _ = registry.Require("api")
	if Allowed(ctx, p) || Allowed(authn.WithActor(ctx, authn.Actor{ID: "admin", Role: authn.Admin}), p) {
		t.Fatal("empty roles restored default access")
	}
	failure = errors.New("database unavailable")
	if _, err = registry.Require("api"); err == nil || registry.List()[0].Available {
		t.Fatal("policy read failure allowed selection")
	}
	if _, ok := registry.Get("unknown"); ok {
		t.Fatal("unknown profile exists")
	}
	if _, err = base.Require("api"); err != nil {
		t.Fatal("base registry was mutated")
	}
	failure = nil
	policy = AccessPolicy{}
	if _, err = registry.Require("api"); err != nil {
		t.Fatal("absent policy should preserve configuration")
	}
}

func modelOptionsCatalog(t *testing.T) *Registry {
	t.Helper()
	t.Setenv("MODEL_ROLES_CLI_AVAILABLE", "true")
	return NewCatalog("cluster", []ai.ModelProfile{{
		ID: "cli", DisplayName: "CLI", Provider: "claude-cli", Runtime: "cli", Scope: "local", Enabled: true, Model: "opus",
		AvailabilityEnvironmentVariable: "MODEL_ROLES_CLI_AVAILABLE", SupportedVersionPrefix: "2.",
		ModelOptions: []ai.ModelOption{{ID: "haiku"}, {ID: "opus", ReasoningEfforts: []string{"low"}}},
	}})
}

func optionIDs(p ai.ModelProfile) []string {
	ids := []string{}
	for _, option := range p.ModelOptions {
		ids = append(ids, option.ID)
	}
	return ids
}

func TestModelRolesNarrowEachRoleToItsModels(t *testing.T) {
	base := modelOptionsCatalog(t)
	policy := AccessPolicy{Profiles: map[string]Access{"cli": {Enabled: true, AllowedRoles: []string{"admin", "tester", "player"}, Models: map[string]ModelAccess{
		"opus":    {AllowedRoles: []string{"admin", "tester"}},
		"retired": {AllowedRoles: []string{"player"}},
	}}}}
	registry := base.WithAccessPolicy(func() (AccessPolicy, error) { return policy, nil })
	p, err := registry.Require("cli")
	if err != nil {
		t.Fatal(err)
	}
	player := authn.WithActor(context.Background(), authn.Actor{ID: "friend", Role: authn.Player})
	tester := authn.WithActor(context.Background(), authn.Actor{ID: "tester", Role: authn.Tester})
	if !ModelAllowed(player, p, "haiku") || ModelAllowed(player, p, "opus") || !ModelAllowed(tester, p, "opus") {
		t.Fatal("per-model roles not applied")
	}
	if _, restricted := p.ModelRoles["retired"]; restricted {
		t.Fatal("entry for a model that is not configured was kept")
	}
	narrowed, ok := ForRole(player, p)
	if !ok || narrowed.Model != "haiku" || narrowed.ModelLabel != "haiku" || !slices.Equal(optionIDs(narrowed), []string{"haiku"}) {
		t.Fatalf("player view: %+v %v", narrowed, ok)
	}
	if full, ok := ForRole(tester, p); !ok || full.Model != "opus" || !slices.Equal(optionIDs(full), []string{"haiku", "opus"}) {
		t.Fatalf("tester view: %+v %v", full, ok)
	}
	if configured, _ := base.Get("cli"); configured.ModelRoles != nil || len(configured.ModelOptions) != 2 || configured.Model != "opus" {
		t.Fatalf("configuration was mutated: %+v", configured)
	}
}

func TestModelRolesSavedUnderAnAliasApplyToItsOption(t *testing.T) {
	t.Setenv("MODEL_ROLES_CLI_AVAILABLE", "true")
	registry := NewCatalog("cluster", []ai.ModelProfile{{
		ID: "cli", DisplayName: "CLI", Provider: "claude-cli", Runtime: "cli", Scope: "local", Enabled: true, Model: "claude-sonnet-5-5",
		AvailabilityEnvironmentVariable: "MODEL_ROLES_CLI_AVAILABLE", SupportedVersionPrefix: "2.",
		ModelOptions: []ai.ModelOption{{ID: "claude-sonnet-5-5", Aliases: []string{"sonnet"}}, {ID: "claude-fable-5-2", Aliases: []string{"fable"}}},
	}})
	// A rule saved while the catalog listed aliases keeps restricting the option that replaced it.
	policy := AccessPolicy{Profiles: map[string]Access{"cli": {Enabled: true, AllowedRoles: []string{"admin", "player"}, Models: map[string]ModelAccess{
		"fable": {AllowedRoles: []string{"admin"}},
	}}}}
	p, err := registry.WithAccessPolicy(func() (AccessPolicy, error) { return policy, nil }).Require("cli")
	if err != nil {
		t.Fatal(err)
	}
	player := authn.WithActor(context.Background(), authn.Actor{ID: "friend", Role: authn.Player})
	admin := authn.WithActor(context.Background(), authn.Actor{ID: "admin", Role: authn.Admin})
	for _, model := range []string{"fable", "claude-fable-5-2"} {
		if ModelAllowed(player, p, model) || !ModelAllowed(admin, p, model) {
			t.Fatalf("%s: alias rule not applied to its option", model)
		}
	}
	if view, ok := ForRole(player, p); !ok || !slices.Equal(optionIDs(view), []string{"claude-sonnet-5-5"}) {
		t.Fatalf("player view: %+v %v", view, ok)
	}
	// An entry under the option's own ID wins over one under its alias.
	models := OptionModels(p, map[string]ModelAccess{"fable": {AllowedRoles: []string{"admin"}}, "claude-fable-5-2": {AllowedRoles: []string{"player"}}, "gone": {}})
	if len(models) != 1 || !slices.Equal(models["claude-fable-5-2"].AllowedRoles, []string{"player"}) {
		t.Fatalf("option models: %+v", models)
	}
	if OptionModels(p, map[string]ModelAccess{"gone": {}}) != nil {
		t.Fatal("entries for unconfigured models were kept")
	}
}

func TestModelRolesDenyWithinTheProfileRoles(t *testing.T) {
	policy := AccessPolicy{Profiles: map[string]Access{"cli": {Enabled: true, AllowedRoles: []string{"admin", "tester", "player"}, Models: map[string]ModelAccess{
		"haiku": {AllowedRoles: []string{"admin"}}, "opus": {AllowedRoles: []string{}},
	}}}}
	registry := modelOptionsCatalog(t).WithAccessPolicy(func() (AccessPolicy, error) { return policy, nil })
	p, _ := registry.Require("cli")
	player := authn.WithActor(context.Background(), authn.Actor{ID: "friend", Role: authn.Player})
	admin := authn.WithActor(context.Background(), authn.Actor{ID: "admin", Role: authn.Admin})
	if _, ok := ForRole(player, p); ok {
		t.Fatal("player kept a profile without an allowed model")
	}
	if ModelAllowed(admin, p, "opus") {
		t.Fatal("empty model roles allowed an administrator")
	}
	if adminView, ok := ForRole(admin, p); !ok || adminView.Model != "haiku" {
		t.Fatalf("admin view: %+v %v", adminView, ok)
	}

	policy.Profiles["cli"] = Access{Enabled: true, AllowedRoles: []string{"admin"}, Models: map[string]ModelAccess{"haiku": {AllowedRoles: []string{"admin", "player"}}}}
	p, _ = registry.Require("cli")
	if ModelAllowed(player, p, "haiku") {
		t.Fatal("model roles widened the profile's roles")
	}
	if !ModelAllowed(admin, p, "unconfigured") {
		t.Fatal("a model without an entry should follow the profile's roles")
	}
}

func TestStoredPolicyWithoutModelEntriesAllowsEveryModel(t *testing.T) {
	var policy AccessPolicy
	if err := json.Unmarshal([]byte(`{"providers":{"claude-cli":true},"profiles":{"cli":{"enabled":true,"allowedRoles":["player"]}}}`), &policy); err != nil {
		t.Fatal(err)
	}
	p, err := modelOptionsCatalog(t).WithAccessPolicy(func() (AccessPolicy, error) { return policy, nil }).Require("cli")
	if err != nil {
		t.Fatal(err)
	}
	player := authn.WithActor(context.Background(), authn.Actor{ID: "friend", Role: authn.Player})
	view, ok := ForRole(player, p)
	if !ok || view.Model != "opus" || !slices.Equal(optionIDs(view), []string{"haiku", "opus"}) {
		t.Fatalf("player view: %+v %v", view, ok)
	}
	if raw, _ := json.Marshal(policy); strings.Contains(string(raw), "models") {
		t.Fatalf("a policy without model entries gained a models field: %s", raw)
	}
	if _, ok := ForRole(authn.WithActor(context.Background(), authn.Actor{ID: "tester", Role: authn.Tester}), p); ok {
		t.Fatal("a role the profile denies kept the profile")
	}
}
