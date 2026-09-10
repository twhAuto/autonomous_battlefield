package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/worker"
)

func TestExpireBattlesAgainstPostgres(t *testing.T) {
	ctx, db := integrationDatabase(t)
	repository := NewPostgres(db)
	for _, test := range []struct {
		status      string
		paused, old bool
	}{
		{"queued", true, true}, {"queued", false, true},
		{"running", true, true}, {"running", false, true},
		{"running", true, false}, {"queued", true, false},
		{"completed", false, true}, {"stopped", false, true}, {"failed", false, true},
	} {
		id := randomUUID(t)
		t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM battle_runs WHERE id=$1`, id) })
		scenario := repositoryTestScenario()
		if err := repository.CreateBattle(ctx, battleservice.CreateBattleParams{ID: id, ScenarioID: scenario.ID, ScenarioDigest: "digest", ScenarioSnapshot: scenario, RequestedTicks: 2}); err != nil {
			t.Fatal(err)
		}
		age := "30 minutes"
		if test.old {
			age = "61 minutes"
		}
		if _, err := db.Exec(ctx, `UPDATE battle_runs SET status=$2, paused=$3, created_at=now()-$4::interval, attempt=1, lease_owner=CASE WHEN $2='running' THEN 'expiry-test' END, lease_expires_at=CASE WHEN $2='running' THEN now()+interval '1 minute' END WHERE id=$1`, id, test.status, test.paused, age); err != nil {
			t.Fatal(err)
		}
		decisionStatus := "pending"
		if test.status == "running" {
			decisionStatus = "running"
		}
		decisionID := randomUUID(t)
		if _, err := db.Exec(ctx, `INSERT INTO battle_decisions (id,battle_id,attempt,faction_id,target_tick,status,controller_type,prompt_version,tool_schema_version,observation,observation_hash) VALUES ($1,$2,1,$3,1,$4,'scripted','test','test','{}','test')`, decisionID, id, randomUUID(t), decisionStatus); err != nil {
			t.Fatal(err)
		}
		if err := repository.ExpireBattles(ctx); err != nil {
			t.Fatal(err)
		}
		run, err := repository.GetBattle(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		expired := test.old && (test.status == "queued" || test.status == "running")
		decision, err := repository.GetDecision(ctx, decisionID)
		if err != nil {
			t.Fatal(err)
		}
		if expired {
			if decision.Status != "failed" || decision.ErrorCategory != "battle_stopped" {
				t.Fatalf("decision not cancelled: %#v", decision)
			}
			if run.Status != "failed" || (run.ErrorCode == nil || *run.ErrorCode != "battle_timeout") || run.Paused {
				t.Fatalf("expired battle: %#v", run)
			}
			if err := repository.HeartbeatBattle(ctx, worker.BattleLease{BattleID: id, Attempt: 1, Owner: "expiry-test"}, time.Minute); !errors.Is(err, worker.ErrBattleLeaseLost) {
				t.Fatalf("stale lease: %v", err)
			}
		} else if decision.Status != decisionStatus || run.Status != test.status || run.Paused != test.paused {
			t.Fatalf("unaffected battle changed: %#v", run)
		}
		if err := repository.ExpireBattles(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
