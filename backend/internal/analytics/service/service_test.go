// Checks report permissions, filters, truncation and fallback exclusion.

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/analytics"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
)

type fakeRepo struct {
	battles []analytics.Battle
	err     error
	filter  Filter
	limit   int
}

func (r *fakeRepo) CompletedBattles(_ context.Context, filter Filter, limit int) ([]analytics.Battle, error) {
	r.filter, r.limit = filter, limit
	return r.battles, r.err
}

func inspector() context.Context {
	return authn.WithActor(context.Background(), authn.Actor{ID: "tester", Role: authn.Tester})
}

func completed(winner string, fallback bool) analytics.Battle {
	b := analytics.Battle{ScenarioID: "frontier", Reason: "castle_destroyed", WinnerFactionID: &winner, TicksRun: 90, Sides: [2]analytics.Side{
		{FactionID: "a", Controller: game.ControllerConfig{Type: "scripted"}}, {FactionID: "b", Controller: game.ControllerConfig{Type: "scripted"}},
	}}
	if fallback {
		b.Sides[1].Decisions.Fallbacks = 1
	}
	return b
}

func TestReportAppliesTheCurrentRulesByDefault(t *testing.T) {
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	repo := &fakeRepo{battles: []analytics.Battle{completed("a", false), completed("b", true)}}
	report, err := New(repo, "9").Report(inspector(), Query{LeagueID: "9D7A1D2C-6C43-4CF8-9A6E-0C5A1F3B2E10", Since: &since})
	if err != nil {
		t.Fatal(err)
	}
	if repo.filter.RulesVersion != "9" || repo.filter.LeagueID != "9d7a1d2c-6c43-4cf8-9a6e-0c5a1f3b2e10" || repo.filter.Since != &since || repo.limit != MaxBattles+1 {
		t.Fatalf("filter = %+v, limit %d", repo.filter, repo.limit)
	}
	if *report.RulesVersion != "9" || report.Battles != 2 || report.BattlesWithFallback != 1 || report.Truncated || report.ExcludedForFallback != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestReportFiltersFallbacksAllVersionsAndTruncates(t *testing.T) {
	repo := &fakeRepo{battles: []analytics.Battle{completed("a", false), completed("b", true)}}
	report, err := New(repo, "9").Report(inspector(), Query{RulesVersion: AllRulesVersions, ExcludeFallback: true})
	if err != nil || repo.filter.RulesVersion != "" || report.RulesVersion != nil || report.Battles != 1 || report.ExcludedForFallback != 1 {
		t.Fatalf("report = %+v, filter %+v, err %v", report, repo.filter, err)
	}
	if report, _ := New(repo, "").Report(inspector(), Query{}); report.RulesVersion != nil || repo.filter.RulesVersion != "" {
		t.Fatalf("no current rules: report %+v, filter %+v", report, repo.filter)
	}
	many := make([]analytics.Battle, MaxBattles+1)
	for i := range many {
		many[i] = completed("a", false)
	}
	report, err = New(&fakeRepo{battles: many}, "9").Report(inspector(), Query{RulesVersion: "8"})
	if err != nil || !report.Truncated || report.Battles != MaxBattles || *report.RulesVersion != "8" {
		t.Fatalf("truncated report: %d battles, truncated %v, %v", report.Battles, report.Truncated, err)
	}
}

func TestReportRejectsServiceIdentitiesInvalidLeaguesAndStorageErrors(t *testing.T) {
	for _, role := range []authn.Role{authn.Player, authn.Guest} {
		if _, err := New(&fakeRepo{}, "9").Report(authn.WithActor(context.Background(), authn.Actor{ID: "viewer", Role: role}), Query{}); err != nil {
			t.Fatalf("%s err = %v", role, err)
		}
	}
	ci := authn.WithActor(context.Background(), authn.Actor{ID: "ci", Role: authn.CI})
	if _, err := New(&fakeRepo{}, "9").Report(ci, Query{}); !errors.Is(err, authn.ErrForbidden) {
		t.Fatalf("service identity err = %v", err)
	}
	if _, err := New(&fakeRepo{}, "9").Report(context.Background(), Query{}); !errors.Is(err, authn.ErrForbidden) {
		t.Fatalf("anonymous err = %v", err)
	}
	if _, err := New(&fakeRepo{}, "9").Report(inspector(), Query{LeagueID: "league"}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("league err = %v", err)
	}
	failure := errors.New("database down")
	if _, err := New(&fakeRepo{err: failure}, "9").Report(inspector(), Query{}); !errors.Is(err, failure) {
		t.Fatalf("storage err = %v", err)
	}
}
