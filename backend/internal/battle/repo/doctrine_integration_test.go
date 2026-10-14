// Exercises faction doctrine persistence and tick-zero decisions against PostgreSQL.

package repo

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/prompts"
	"github.com/twh09/autonomous_battlefield/backend/internal/testutil"

	"context"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	factions "github.com/twh09/autonomous_battlefield/backend/internal/faction"
	factionrepo "github.com/twh09/autonomous_battlefield/backend/internal/faction/repo"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func TestDoctrinePersistenceAndTickZeroAgainstPostgres(t *testing.T) {
	ctx, db := integrationDatabase(t)
	ctx = authn.WithActor(ctx, authn.Actor{ID: "test-admin", Role: authn.Admin})
	id := randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM factions WHERE id=$1`, id) })
	profiles := factionservice.NewService(factionrepo.NewPostgres(db))
	{
		err := profiles.CreateFaction(ctx, factionservice.CreateInput{ID: id, Name: " A ", Doctrine: " Scout "})
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	{
		err := profiles.(factionservice.Updater).UpdateFaction(ctx, factionservice.CreateInput{ID: id, Name: "B", Doctrine: "Defend"})
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	stored, err := factionrepo.NewPostgres(db).GetFaction(ctx, id)
	testutil.Require(t, !(err != nil || stored.Doctrine != "Defend" || stored.Name != "B"), func() { t.Fatalf("profile=%+v %v", stored, err) })
	list, err := profiles.ListFactions(ctx)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	found := false
	for _, p := range list {
		if p.ID == id {
			found = p.Doctrine == stored.Doctrine
		}
	}
	testutil.Require(t, found, func() { t.Fatal("doctrine missing in list") })
	// Exercise the migration's default on a legacy-style INSERT while retaining its data.
	legacy := randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM factions WHERE id=$1`, legacy) })
	_, err = db.Exec(ctx, `INSERT INTO factions(id,name,ruleset_id,blueprint) VALUES($1,'Legacy','old','{"units":[]}'::jsonb)`, legacy)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	old, err := factionrepo.NewPostgres(db).GetFaction(ctx, legacy)
	testutil.Require(t, !(err != nil || old.Doctrine != factions.BalancedDoctrine || old.RulesetID != "old"), func() { t.Fatalf("legacy %+v %v", old, err) })
	battleID := randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM battle_runs WHERE id=$1`, battleID) })
	repo := NewPostgres(db)
	job := createAndClaimBattle(t, ctx, repo, battleID)
	orders := []controller.RecruitmentOrder{}
	plan := controller.PlanProposal{Name: "save", RecruitmentOrders: &orders}
	params := ai.CreateDecisionParams{ID: randomUUID(t), BattleID: battleID, Attempt: job.Lease.Attempt, FactionID: id, TargetTick: 0, ControllerType: ai.ControllerScripted, Observation: ai.DecisionContext{Opening: true, Doctrine: "Scout"}, ObservationHash: "opening", PromptVersion: prompts.Version, ToolSchemaVersion: ai.ToolSchema}
	record, err := repo.CreateOrReuseDecision(ctx, params)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	pending, err := repo.CountPendingWindowDecisions(ctx, battleID, job.Lease.Attempt, 0)
	testutil.Require(t, err == nil && pending == 1, func() { t.Fatalf("pending before submission: %d %v", pending, err) })
	submission := ai.PlanSubmission{RequestID: record.ID, ExpectedVersion: record.Version, TargetTick: 0, IdempotencyKey: "opening", Plan: plan}
	for range 2 {
		result, err := repo.SubmitDecisionPlan(ctx, submission)
		testutil.Require(t, !(err != nil || !result.Accepted), func() { t.Fatalf("submit %+v %v", result, err) })
	}
	pending, err = repo.CountPendingWindowDecisions(ctx, battleID, job.Lease.Attempt, 0)
	testutil.Require(t, err == nil && pending == 0, func() { t.Fatalf("pending after acceptance: %d %v", pending, err) })
	transition := game.Transition{Tick: 0, State: game.State{BattleID: battleID, OpeningComplete: true}, Events: []game.Event{{Sequence: 1, Tick: 0, Type: "opening_completed", Data: map[string]any{}}}}
	for range 2 {
		{
			err := repo.SaveTick(ctx, job.Lease, transition, time.Minute)
			testutil.Require(t, err == nil, func() { t.Fatal(err) })
		}
	}
	events, err := repo.ListBattleEvents(ctx, battleID, job.Lease.Attempt, 0, 100, true)
	testutil.Require(t, !(err != nil || len(events) != 1), func() { t.Fatalf("duplicate opening events: %d %v", len(events), err) })
}
