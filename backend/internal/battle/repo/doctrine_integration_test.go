// Exercises faction doctrine persistence and tick-zero decisions against PostgreSQL.

package repo

import (
	"context"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	factions "github.com/twh09/autonomous_battlefield/backend/internal/faction"
	factionrepo "github.com/twh09/autonomous_battlefield/backend/internal/faction/repo"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"testing"
	"time"
)

func TestDoctrinePersistenceAndTickZeroAgainstPostgres(t *testing.T) {
	ctx, db := integrationDatabase(t)
	id := randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM factions WHERE id=$1`, id) })
	profiles := factionservice.NewService(factionrepo.NewPostgres(db))
	if err := profiles.CreateFaction(ctx, factionservice.CreateInput{ID: id, Name: " A ", Doctrine: " Scout "}); err != nil {
		t.Fatal(err)
	}
	if err := profiles.(factionservice.Updater).UpdateFaction(ctx, factionservice.CreateInput{ID: id, Name: "B", Doctrine: "Defend"}); err != nil {
		t.Fatal(err)
	}
	stored, err := factionrepo.NewPostgres(db).GetFaction(ctx, id)
	if err != nil || stored.Doctrine != "Defend" || stored.Name != "B" {
		t.Fatalf("profile=%+v %v", stored, err)
	}
	list, err := profiles.ListFactions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range list {
		if p.ID == id {
			found = p.Doctrine == stored.Doctrine
		}
	}
	if !found {
		t.Fatal("doctrine missing in list")
	}
	// Exercise the migration's default on a legacy-style INSERT while retaining its data.
	legacy := randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM factions WHERE id=$1`, legacy) })
	_, err = db.Exec(ctx, `INSERT INTO factions(id,name,ruleset_id,blueprint) VALUES($1,'Legacy','old','{"units":[]}'::jsonb)`, legacy)
	if err != nil {
		t.Fatal(err)
	}
	old, err := factionrepo.NewPostgres(db).GetFaction(ctx, legacy)
	if err != nil || old.Doctrine != factions.BalancedDoctrine || old.RulesetID != "old" {
		t.Fatalf("legacy %+v %v", old, err)
	}
	battleID := randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM battle_runs WHERE id=$1`, battleID) })
	repo := NewPostgres(db)
	job := createAndClaimBattle(t, ctx, repo, battleID)
	orders := []controller.RecruitmentOrder{}
	plan := controller.PlanProposal{Name: "save", RecruitmentOrders: &orders}
	params := ai.CreateDecisionParams{ID: randomUUID(t), BattleID: battleID, Attempt: job.Lease.Attempt, FactionID: id, TargetTick: 0, ControllerType: ai.ControllerScripted, Observation: ai.DecisionContext{Opening: true, Doctrine: "Scout"}, ObservationHash: "opening", PromptVersion: ai.PromptVersion, ToolSchemaVersion: ai.ToolSchema}
	record, err := repo.CreateOrReuseDecision(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	submission := ai.PlanSubmission{RequestID: record.ID, ExpectedVersion: record.Version, TargetTick: 0, IdempotencyKey: "opening", Plan: plan}
	for range 2 {
		result, err := repo.SubmitDecisionPlan(ctx, submission)
		if err != nil || !result.Accepted {
			t.Fatalf("submit %+v %v", result, err)
		}
	}
	transition := game.Transition{Tick: 0, State: game.State{BattleID: battleID, OpeningComplete: true}, Events: []game.Event{{Sequence: 1, Tick: 0, Type: "opening_completed", Data: map[string]any{}}}}
	for range 2 {
		if err := repo.SaveTick(ctx, job.Lease, transition, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	events, err := repo.ListBattleEvents(ctx, battleID, job.Lease.Attempt, 0, 100, true)
	if err != nil || len(events) != 1 {
		t.Fatalf("duplicate opening events: %d %v", len(events), err)
	}
}
