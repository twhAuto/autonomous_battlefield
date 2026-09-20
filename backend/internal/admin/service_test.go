package admin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
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
	repo := &policyRepository{settings: Settings{Policy: ai.AccessPolicy{Providers: map[string]bool{"openai": false}}}}
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
	view.Policy.Profiles["cli"] = ai.ProfileAccess{Enabled: true, AllowedRoles: []string{"player"}}
	if _, err := service.Save(context.Background(), view.Settings, "administrator"); !errors.Is(err, ErrInvalid) || repo.saves != 0 {
		t.Fatalf("invalid policy persisted: saves=%d err=%v", repo.saves, err)
	}
	view.Policy.Profiles["cli"] = ai.ProfileAccess{Enabled: true, AllowedRoles: []string{"admin"}}
	saved, err := service.Save(context.Background(), view.Settings, "administrator")
	if err != nil || saved.Version != 1 || repo.saves != 1 || repo.actor != "administrator" {
		t.Fatalf("save lost version or actor: %+v repo=%+v err=%v", saved, repo, err)
	}
	repo.err = ErrConflict
	if _, err := service.Save(context.Background(), view.Settings, "administrator"); !errors.Is(err, ErrConflict) {
		t.Fatalf("write conflict lost: %v", err)
	}
}
