// Checks decision SQL reads, claims, retry reuse, versioned/idempotent writes, and scoped
// memory queries.

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/briefing"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func decisionTestRow(status string, plan *controller.PlanProposal) battleRow {
	observation, _ := json.Marshal(ai.DecisionContext{RequestID: "11111111-1111-1111-1111-111111111111", Legal: ai.LegalChoices{ActionKinds: []string{"attack", "defend"}}})
	var planJSON []byte
	if plan != nil {
		planJSON, _ = json.Marshal(plan)
	}
	usage, _ := json.Marshal(ai.Usage{Requests: 1})
	metadata := []byte(`{"safe":true}`)
	now := time.Now()
	return func(destinations ...any) error {
		return assignBattleValues(destinations, []any{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", 1, "33333333-3333-3333-3333-333333333333", uint64(10), int64(1), status, "model", "profile", "openai", "api", "model", "prompt-v1", ai.ToolSchema, observation, "hash", planJSON, "", "", usage, metadata, "", "00-test-trace", "test-state", now, (*time.Time)(nil), &now, "", (*time.Time)(nil), (*time.Time)(nil), (*time.Time)(nil)})
	}
}

func TestDecisionReadsAndClaims(t *testing.T) {
	plan := controller.PlanProposal{Name: "advance"}
	db := &battleDB{row: decisionTestRow("accepted", &plan)}
	repository := NewPostgres(db)
	record, err := repository.GetDecision(context.Background(), "id")
	if err != nil || record.ResolvedPlan == nil || record.Usage.Requests != 1 {
		t.Fatalf("record=%#v err=%v", record, err)
	}
	db.row = decisionTestRow("running", nil)
	claimed, err := repository.ClaimModelDecision(context.Background(), "agent", []string{"profile"}, time.Second)
	if err != nil || claimed.Status != "running" {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	if !strings.Contains(db.queryRowSQL, "claimed AS") || !strings.Contains(db.queryRowSQL, "profile_id = ANY") || strings.Contains(db.queryRowSQL, "RETURNING id,") {
		t.Fatalf("claim query leaves UPDATE columns ambiguous: %s", db.queryRowSQL)
	}
	if !strings.Contains(db.queryRowSQL, "lease_expires_at=d.created_at+$2::interval") || !strings.Contains(db.queryRowSQL, "created_at + $2::interval >= now() + $4::interval") {
		t.Fatalf("claim lease must end with the decision window: %s", db.queryRowSQL)
	}
	db.row = battleRow(func(...any) error { return pgx.ErrNoRows })
	if _, err := repository.ClaimModelDecision(context.Background(), "agent", []string{"profile"}, time.Second); !errors.Is(err, ai.ErrNoDecision) {
		t.Fatal(err)
	}
	if _, err := repository.ClaimModelDecision(context.Background(), "agent", nil, time.Second); !errors.Is(err, ai.ErrNoDecision) {
		t.Fatal(err)
	}
	if _, err := repository.GetDecision(context.Background(), "missing"); !errors.Is(err, ai.ErrDecisionNotFound) {
		t.Fatal(err)
	}
}

func TestDecisionListAndMutations(t *testing.T) {
	rows := &battleRows{scans: []battleRow{decisionTestRow("pending", nil), decisionTestRow("accepted", nil)}}
	db := &battleDB{rows: rows, execTag: pgconn.NewCommandTag("UPDATE 1")}
	repository := NewPostgres(db)
	window, err := repository.ListWindowDecisions(context.Background(), "battle", 1, 10)
	if err != nil || len(window) != 2 {
		t.Fatalf("window=%#v err=%v", window, err)
	}
	db.rows = &battleRows{scans: []battleRow{decisionTestRow("accepted", nil)}}
	history, err := repository.ListBattleDecisions(context.Background(), "battle", 1)
	if err != nil || len(history) != 1 {
		t.Fatal(err)
	}
	accepted, err := repository.ResolveDecisionFallback(context.Background(), "id", 1, controller.PlanProposal{Name: "fallback"}, "previous_plan", "timeout")
	if err != nil || !accepted {
		t.Fatalf("accepted=%v err=%v", accepted, err)
	}
	if err := repository.FinishDecisionFailure(context.Background(), "id", "provider", ai.Usage{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordDecisionUsage(context.Background(), "id", ai.Usage{Requests: 1}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRecordDecisionValidationReturnsUpdatedCount(t *testing.T) {
	db := &battleDB{}
	repository := NewPostgres(db)
	if failures, err := repository.RecordDecisionValidation(context.Background(), "closed"); err != nil || failures != 0 {
		t.Fatalf("closed decision: failures=%d err=%v", failures, err)
	}
	db.row = battleRow(func(destinations ...any) error { *(destinations[0].(*int)) = 3; return nil })
	if failures, err := repository.RecordDecisionValidation(context.Background(), "open"); err != nil || failures != 3 || !strings.Contains(db.queryRowSQL, "RETURNING") {
		t.Fatalf("open decision: failures=%d err=%v", failures, err)
	}
	want := errors.New("database")
	db.row = battleRow(func(...any) error { return want })
	if _, err := repository.RecordDecisionValidation(context.Background(), "open"); !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func TestSubmitDecisionPlanSuccessIdempotencyAndConflict(t *testing.T) {
	submission := ai.PlanSubmission{RequestID: "id", IdempotencyKey: "once", ExpectedVersion: 1, TargetTick: 10, Plan: controller.PlanProposal{Name: "advance"}}
	repository := NewPostgres(&battleDB{row: battleRow(func(destinations ...any) error { *(destinations[0].(*string)) = "accepted"; return nil })})
	result, err := repository.SubmitDecisionPlan(context.Background(), submission)
	if err != nil || !result.Accepted {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	rowStatus := func(status, key string) battleRow {
		return func(destinations ...any) error {
			*(destinations[0].(*string)) = status
			*(destinations[1].(*string)) = key
			return nil
		}
	}
	db := &battleDB{rowQueue: []pgx.Row{battleRow(func(...any) error { return pgx.ErrNoRows }), rowStatus("accepted", "once")}}
	result, err = NewPostgres(db).SubmitDecisionPlan(context.Background(), submission)
	if err != nil || !result.Accepted {
		t.Fatal(err)
	}
	db = &battleDB{rowQueue: []pgx.Row{battleRow(func(...any) error { return pgx.ErrNoRows }), rowStatus("fallback", "other")}}
	result, err = NewPostgres(db).SubmitDecisionPlan(context.Background(), submission)
	if err != nil || result.Accepted || len(result.Errors) == 0 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestCreateDecisionAndRetryReuse(t *testing.T) {
	params := ai.CreateDecisionParams{ID: "11111111-1111-1111-1111-111111111111", BattleID: "22222222-2222-2222-2222-222222222222", Attempt: 1, FactionID: "33333333-3333-3333-3333-333333333333", TargetTick: 10, ControllerType: "model", PromptVersion: "v1", ToolSchemaVersion: ai.ToolSchema, Observation: ai.DecisionContext{RequestID: "id"}, ObservationHash: "hash"}
	db := &battleDB{rowQueue: []pgx.Row{battleRow(func(...any) error { return pgx.ErrNoRows }), decisionTestRow("pending", nil)}}
	record, err := NewPostgres(db).CreateOrReuseDecision(context.Background(), params)
	if err != nil || record.Status != "pending" {
		t.Fatalf("record=%#v err=%v", record, err)
	}
	if !strings.Contains(db.queryRowSQL, "pg_notify('"+ai.DecisionReadyChannel+"'") || !strings.Contains(db.queryRowSQL, "controller_type='model' AND status='pending'") {
		t.Fatalf("decision insert does not emit a durable wake-up notification: %s", db.queryRowSQL)
	}
	plan, _ := json.Marshal(controller.PlanProposal{Name: "reuse"})
	previous := battleRow(func(destinations ...any) error {
		*(destinations[0].(*[]byte)) = plan
		*(destinations[1].(*string)) = "retry_reuse"
		return nil
	})
	db = &battleDB{rowQueue: []pgx.Row{previous, decisionTestRow("accepted", nil)}}
	if _, err := NewPostgres(db).CreateOrReuseDecision(context.Background(), params); err != nil {
		t.Fatal(err)
	}
}

func TestFactionMemoryQueryScopesAndLimitsBeforeRead(t *testing.T) {
	decisions := &battleRows{scans: []battleRow{decisionTestRow("accepted", &controller.PlanProposal{Name: "reserve", ReserveGold: 10})}}
	events := &battleRows{scans: []battleRow{func(destinations ...any) error {
		return assignBattleValues(destinations, []any{uint64(12), "production_skipped", "33333333-3333-3333-3333-333333333333", "construction", "maximum_reached"})
	}}}
	db := &battleDB{rowsQueue: []pgx.Rows{decisions, events}}
	got, err := NewPostgres(db).ListFactionMemory(context.Background(), "22222222-2222-2222-2222-222222222222", 1, "33333333-3333-3333-3333-333333333333", 20)
	if err != nil || len(got) != 1 || got[0].Plan.ReserveGold != 10 {
		t.Fatalf("memory=%+v err=%v", got, err)
	}
	if outcome := got[0].Outcome; outcome == nil || len(outcome.Skipped) != 1 || outcome.Skipped[0].Count != 1 {
		t.Fatalf("plan outcome missing: %+v", got[0].Outcome)
	}
	assertMemoryQueriesScoped(t, db)
	db.queryErr = errors.New("unavailable")
	if _, err := NewPostgres(db).ListFactionMemory(context.Background(), "b", 1, "f", 20); err == nil {
		t.Fatal("query failure lost")
	}
	db.queryErr = nil
	db.rows = &battleRows{scans: []battleRow{func(...any) error { return errors.New("scan") }}}
	if _, err := NewPostgres(db).ListFactionMemory(context.Background(), "b", 1, "f", 20); err == nil {
		t.Fatal("scan failure lost")
	}
}

// The decision read is scoped and limited; the outcome read covers only its windows.
func assertMemoryQueriesScoped(t *testing.T, db *battleDB) {
	t.Helper()
	if len(db.queries) != 2 {
		t.Fatalf("queries = %v", db.queries)
	}
	for _, part := range []string{"battle_id=$1", "attempt=$2", "faction_id=$3", "target_tick<$4", "ORDER BY target_tick DESC LIMIT $5"} {
		if !strings.Contains(db.queries[0], part) {
			t.Fatalf("unscoped query: %s", db.queries[0])
		}
	}
	if args := db.queryArgLog[0]; len(args) != 5 || args[4] != briefing.FactionMemoryLimit {
		t.Fatal("unbounded query")
	}
	for _, part := range []string{"battle_events", "battle_id=$1", "attempt=$2", "tick>=$3", "tick<$4"} {
		if !strings.Contains(db.queries[1], part) {
			t.Fatalf("unscoped outcome query: %s", db.queries[1])
		}
	}
	if args := db.queryArgLog[1]; len(args) != 4 || args[2] != uint64(10) || args[3] != uint64(20) {
		t.Fatalf("outcome window args = %v", args)
	}
}

func TestCountPendingWindowDecisions(t *testing.T) {
	db := &battleDB{row: battleRow(func(destinations ...any) error { *(destinations[0].(*int)) = 2; return nil })}
	count, err := NewPostgres(db).CountPendingWindowDecisions(context.Background(), "battle", 1, 10)
	if err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	// The poll must stay cheap: count unresolved rows only, never the frozen observations.
	if !strings.Contains(db.queryRowSQL, "count(*)") || !strings.Contains(db.queryRowSQL, "status IN ('pending','running')") || strings.Contains(db.queryRowSQL, "observation") {
		t.Fatalf("query=%s", db.queryRowSQL)
	}
	want := errors.New("database")
	if _, err := NewPostgres(&battleDB{row: battleRow(func(...any) error { return want })}).CountPendingWindowDecisions(context.Background(), "battle", 1, 10); !errors.Is(err, want) {
		t.Fatal(err)
	}
}
