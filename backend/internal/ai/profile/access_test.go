package profile

import (
	"context"
	"errors"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
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
