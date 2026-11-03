// Checks stable history pagination, rejection of invalid filters and cursors, and the
// rules-version listing.

package service

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func TestBattleHistoryPagination(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	id := "00000000-0000-0000-0000-000000000002"
	repo := &fakeBattleRepo{summaries: []BattleSummary{{ID: id, CreatedAt: now}, {ID: "00000000-0000-0000-0000-000000000001", CreatedAt: now}}}
	service := &battleService{battles: repo}
	page, err := service.ListBattles(context.Background(), BattleHistoryInput{Limit: 1})
	if err != nil || len(page.Battles) != 1 || page.NextCursor == nil || repo.historyQuery.Limit != 2 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	repo.summaries = repo.summaries[1:]
	next, err := service.ListBattles(context.Background(), BattleHistoryInput{Limit: 1, Cursor: *page.NextCursor})
	if err != nil || next.NextCursor != nil || repo.historyQuery.Before.ID != id || !repo.historyQuery.Before.CreatedAt.Equal(now) {
		t.Fatalf("next=%+v query=%+v err=%v", next, repo.historyQuery, err)
	}
	repo.summaries = nil
	empty, err := service.ListBattles(context.Background(), BattleHistoryInput{Limit: 25})
	if err != nil || empty.Battles == nil || len(empty.Battles) != 0 || empty.NextCursor != nil {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
}

func TestBattleHistoryValidationAndErrors(t *testing.T) {
	repo := &fakeBattleRepo{}
	service := &battleService{battles: repo}
	for _, status := range []string{"", "queued", "running", "paused", "completed", "failed", "stopped"} {
		if _, err := service.ListBattles(context.Background(), BattleHistoryInput{Status: status, Limit: 100}); err != nil || repo.historyQuery.Status != status {
			t.Fatalf("status=%q err=%v", status, err)
		}
	}
	for _, input := range []BattleHistoryInput{
		{Limit: 0}, {Limit: -1}, {Limit: 101}, {Limit: 25, Status: "all"},
		{Limit: 25, RulesVersion: strings.Repeat("9", game.MaxRulesVersionLength+1)},
		{Limit: 25, Cursor: "!"}, {Limit: 25, Cursor: base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", 257)))},
		{Limit: 25, Cursor: base64.RawURLEncoding.EncodeToString([]byte("{"))},
		{Limit: 25, Cursor: base64.RawURLEncoding.EncodeToString([]byte("{}"))},
		{Limit: 25, Cursor: base64.RawURLEncoding.EncodeToString([]byte(`{"createdAt":"2026-09-07T00:00:00Z","id":"bad"}`))},
	} {
		if _, err := service.ListBattles(context.Background(), input); !errors.Is(err, ErrInvalidBattle) {
			t.Fatalf("input=%+v err=%v", input, err)
		}
	}
	if _, err := service.ListBattles(context.Background(), BattleHistoryInput{Limit: 25, RulesVersion: "13"}); err != nil || repo.historyQuery.RulesVersion != "13" {
		t.Fatalf("rules version query=%+v err=%v", repo.historyQuery, err)
	}
	repo.listErr = errors.New("unavailable")
	if _, err := service.ListBattles(context.Background(), BattleHistoryInput{Limit: 25}); !errors.Is(err, repo.listErr) {
		t.Fatal(err)
	}
}

func TestListRulesVersionsIncludesTheCurrentVersionNewestFirst(t *testing.T) {
	repo := &fakeBattleRepo{versions: []string{"9", "12"}}
	catalog := &fakeScenarioCatalog{summaries: []ScenarioSummary{{ID: "frontier-siege", RulesVersion: "13"}}}
	service := &battleService{battles: repo, catalog: catalog}
	got, err := service.ListRulesVersions(context.Background())
	if err != nil || got.Current != "13" || !slices.Equal(got.Versions, []string{"13", "12", "9"}) {
		t.Fatalf("versions=%+v err=%v", got, err)
	}
	repo.versions = []string{"13"}
	if got, err := service.ListRulesVersions(context.Background()); err != nil || !slices.Equal(got.Versions, []string{"13"}) {
		t.Fatalf("recorded current version listed twice: %+v, %v", got, err)
	}
	repo.versions, catalog.summaries = nil, nil
	if got, err := service.ListRulesVersions(context.Background()); err != nil || got.Current != "" || got.Versions == nil || len(got.Versions) != 0 {
		t.Fatalf("empty catalog and history = %+v, %v", got, err)
	}
	repo.listErr = errors.New("unavailable")
	if _, err := service.ListRulesVersions(context.Background()); !errors.Is(err, repo.listErr) {
		t.Fatal(err)
	}
}
