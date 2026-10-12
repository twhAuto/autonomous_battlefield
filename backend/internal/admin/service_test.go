package admin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
)

type policyRepository struct {
	settings Settings
	err      error
	actor    string
	saves    int
	deadline time.Time
}

func (r *policyRepository) Read(ctx context.Context) (Settings, error) {
	r.deadline, _ = ctx.Deadline()
	return r.settings, r.err
}
func (r *policyRepository) Save(_ context.Context, s Settings, actor string) (Settings, error) {
	r.saves++
	r.actor = actor
	s.Version++
	r.settings = s
	return s, r.err
}

func TestPolicySourceWithoutHTTP(t *testing.T) {
	repo := &policyRepository{settings: Settings{Policy: profile.AccessPolicy{Providers: map[string]bool{"openai": false}}}}
	service := &Service{Repository: repo, Profiles: registry()}
	profiles := service.Profiles.WithAccessPolicy(service.ReadPolicy)
	if _, err := profiles.Require("api"); err == nil {
		t.Fatal("persisted provider disable ignored")
	}
	if remaining := time.Until(repo.deadline); remaining <= 0 || remaining > 3*time.Second {
		t.Fatalf("unbounded policy read: %v", remaining)
	}
	repo.settings.Policy.Providers["openai"] = true
	if _, err := profiles.Require("api"); err != nil {
		t.Fatal("policy update was not observed", err)
	}
	repo.err = errors.New("database offline")
	if _, err := service.ReadPolicy(); !errors.Is(err, repo.err) {
		t.Fatalf("policy read error lost: %v", err)
	}
}

func TestSaveValidatesBeforePersistenceAndPreservesActor(t *testing.T) {
	repo := &policyRepository{}
	service := &Service{Repository: repo, Profiles: registry()}
	view, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	view.Policy.Profiles["cli"] = profile.Access{Enabled: true, AllowedRoles: []string{"root"}}
	if _, err := service.Save(context.Background(), view.Settings, "administrator"); !errors.Is(err, ErrInvalid) || repo.saves != 0 {
		t.Fatalf("invalid policy persisted: saves=%d err=%v", repo.saves, err)
	}
	view.Policy.Profiles["cli"] = profile.Access{Enabled: true, AllowedRoles: []string{"admin"}}
	saved, err := service.Save(context.Background(), view.Settings, "administrator")
	if err != nil || saved.Version != 1 || repo.saves != 1 || repo.actor != "administrator" {
		t.Fatalf("save lost version or actor: %+v repo=%+v err=%v", saved, repo, err)
	}
	repo.err = ErrConflict
	if _, err := service.Save(context.Background(), view.Settings, "administrator"); !errors.Is(err, ErrConflict) {
		t.Fatalf("write conflict lost: %v", err)
	}
}

func TestPlayerCLIAccessGrantAndRevocation(t *testing.T) {
	t.Setenv("TEST_CLI_AVAILABLE", "true")
	base := profile.NewCatalog("cluster", []ai.ModelProfile{{ID: "cli", DisplayName: "CLI", Provider: "codex-cli", Runtime: "cli", Scope: "local", Enabled: true, AvailabilityEnvironmentVariable: "TEST_CLI_AVAILABLE", SupportedVersionPrefix: "1."}})
	repo := &policyRepository{}
	service := &Service{Repository: repo, Profiles: base}
	profiles := base.WithAccessPolicy(service.ReadPolicy)
	ctx := authn.WithActor(context.Background(), authn.Actor{ID: "friend", Role: authn.Player})
	check := func(want bool) {
		t.Helper()
		p, err := profiles.Require("cli")
		if err != nil || profile.Allowed(ctx, p) != want {
			t.Fatalf("CLI permission want %v: %+v %v", want, p, err)
		}
	}
	check(false)
	for _, allow := range []bool{true, false} {
		view, err := service.Get(ctx)
		if err != nil {
			t.Fatal(err)
		}
		roles := []string{"admin", "developer"}
		if allow {
			roles = append(roles, "player")
		}
		view.Policy.Profiles["cli"] = profile.Access{Enabled: true, AllowedRoles: roles}
		if _, err := service.Save(ctx, view.Settings, "administrator"); err != nil {
			t.Fatal(err)
		}
		check(allow)
		reloaded, err := service.Get(ctx)
		if err != nil || len(reloaded.Policy.Profiles["cli"].AllowedRoles) != len(roles) {
			t.Fatalf("roles not persisted: %+v %v", reloaded, err)
		}
	}
	// Explicit deployment defaults must be presented faithfully in Admin.
	defaults := defaultProfileAccess(ai.ModelProfile{Runtime: "cli", AllowedRoles: []string{"player"}})
	if len(defaults.AllowedRoles) != 1 || defaults.AllowedRoles[0] != "player" {
		t.Fatal(defaults)
	}
}
