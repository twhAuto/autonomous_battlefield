// Checks that the battle allowance follows the role CreateBattle uses for admission.

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
)

func TestBattleAllowanceFollowsTheCallersRole(t *testing.T) {
	reset := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	stored := Allowance{Limited: true, StartedToday: 2, DailyLimit: 5, Unfinished: 1, UnfinishedLimit: 1, ServiceUnfinished: 3, ServiceUnfinishedLimit: 4, ResetAt: reset}
	for _, tc := range []struct {
		role   authn.Role
		tester bool
		want   Allowance
	}{
		{authn.Player, false, stored},
		{authn.Tester, true, stored},
		{authn.Admin, false, Allowance{StartedToday: 2, Unfinished: 1, ServiceUnfinished: 3, ResetAt: reset}},
	} {
		repo := &fakeBattleRepo{allowance: stored}
		service := NewService(repo, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000, Options{})
		got, err := service.BattleAllowance(authn.WithActor(context.Background(), authn.Actor{ID: "actor", Role: tc.role}))
		if err != nil || got != tc.want {
			t.Fatalf("%s: allowance = %#v, %v; want %#v", tc.role, got, err, tc.want)
		}
		if repo.allowanceOwner != "actor" || repo.allowanceTester != tc.tester {
			t.Fatalf("%s: read owner %q with tester=%v", tc.role, repo.allowanceOwner, repo.allowanceTester)
		}
	}
}

func TestBattleAllowanceReturnsRepositoryErrors(t *testing.T) {
	failure := errors.New("database unavailable")
	service := NewService(&fakeBattleRepo{allowanceErr: failure}, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000, Options{})
	ctx := authn.WithActor(context.Background(), authn.Actor{ID: "admin", Role: authn.Admin})
	if _, err := service.BattleAllowance(ctx); !errors.Is(err, failure) {
		t.Fatalf("error = %v; want %v", err, failure)
	}
}
