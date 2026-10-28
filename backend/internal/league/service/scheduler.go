// Plays league fixtures in the background. Each pass settles finished battles, opens the
// next knockout stage or completes the league, and starts at most one battle, only
// when the engine is idle so a league battle never waits out its timeout in the queue.

package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/league"
)

const (
	// SchedulerInterval is how often each API replica attempts a pass.
	SchedulerInterval = 10 * time.Second
	passTimeout       = 30 * time.Second
)

// errForeignBattle marks a battle that holds a fixture's battle ID but was not created
// for that fixture.
var errForeignBattle = errors.New("battle ID is held by another battle")

// permanentStartErrors are battle-creation failures a retry cannot fix; they spend the
// fixture's attempt. Any other error is retried on the next pass.
var permanentStartErrors = []error{
	errForeignBattle,
	authn.ErrForbidden, battleservice.ErrInvalidBattle, battleservice.ErrBattleAlreadyExists, battleservice.ErrBattleFactionMissing,
	battleservice.ErrScenarioNotFound, battleservice.ErrIncompatibleFaction, battleservice.ErrModelProfileUnavailable, game.ErrInvalidBlueprint,
}

// RunScheduler schedules until ctx ends. Every API replica may run it; the repository
// lock lets one pass run at a time.
func (s *Service) RunScheduler(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		passCtx, cancel := context.WithTimeout(ctx, passTimeout)
		if err := s.SchedulePass(passCtx); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "league_schedule_failed", "error", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// SchedulePass stops battles left running in cancelled leagues, then advances every
// running league once.
func (s *Service) SchedulePass(ctx context.Context) error {
	_, err := s.repo.Schedule(ctx, s.pass)
	return err
}

func (s *Service) pass(ctx context.Context, store SchedulerStore) error {
	if err := s.stopStranded(ctx, store); err != nil {
		return err
	}
	leagues, err := store.RunningLeagues(ctx)
	if err != nil {
		return err
	}
	for i := range leagues {
		if err := settle(ctx, store, &leagues[i]); err != nil {
			return err
		}
	}
	for i := range leagues {
		if next := nextPending(&leagues[i]); next != nil {
			return s.start(ctx, store, &leagues[i], *next)
		}
	}
	return nil
}

// stopStranded retries stops that a cancellation could not complete.
func (s *Service) stopStranded(ctx context.Context, store SchedulerStore) error {
	stranded, err := store.StrandedBattles(ctx)
	if err != nil {
		return err
	}
	for _, battle := range stranded {
		s.stopBattle(ownerContext(ctx, battle.OwnerID), battle.LeagueID, battle.BattleID)
	}
	return nil
}

// settle records finished battles, then opens the next stage or crowns the champion.
func settle(ctx context.Context, store SchedulerStore, l *league.League) error {
	for i := range l.Fixtures {
		if l.Fixtures[i].Status != league.FixtureRunning {
			continue
		}
		next, changed := settledFixture(l.Fixtures[i])
		if !changed {
			continue
		}
		l.Fixtures[i] = next
		if err := store.SaveFixture(ctx, next); err != nil {
			return err
		}
	}
	fixtures, champion := l.Advance()
	if len(fixtures) > 0 {
		if err := store.AddFixtures(ctx, l.ID, fixtures); err != nil {
			return err
		}
		l.Fixtures = append(l.Fixtures, fixtures...)
		slog.InfoContext(ctx, "league_stage_opened", "league_id", l.ID, "stage", fixtures[0].Stage, "fixtures", len(fixtures))
	}
	if champion != nil {
		if err := store.CompleteLeague(ctx, l.ID, *champion); err != nil {
			return err
		}
		l.Status, l.Champion = league.StatusCompleted, champion
		slog.InfoContext(ctx, "league_completed", "league_id", l.ID, "champion_entrant", *champion)
	}
	return nil
}

// settledFixture moves a running fixture on once its battle has finished. A failed or
// stopped battle, or one that no longer exists, spends the attempt: the fixture is
// retried with a new battle, or void after MaxAttempts.
func settledFixture(f league.Fixture) (league.Fixture, bool) {
	status := ""
	if f.Battle != nil {
		status = f.Battle.Status
	}
	switch status {
	case "queued", "running":
		return f, false
	case league.BattleCompleted:
		f.Status = league.FixtureCompleted
	default:
		if f.Attempts >= league.MaxAttempts {
			f.Status = league.FixtureVoid
		} else {
			f.Status, f.BattleID, f.Battle = league.FixturePending, nil, nil
		}
	}
	return f, true
}

func nextPending(l *league.League) *league.Fixture {
	if l.Status != league.StatusRunning {
		return nil
	}
	for i := range l.Fixtures {
		if l.Fixtures[i].Status == league.FixturePending {
			return &l.Fixtures[i]
		}
	}
	return nil
}

// start creates the fixture's battle as the league owner. The battle ID is derived from
// the league's salt, the fixture and the attempt, so a pass interrupted after creating
// it links the same battle next time instead of starting another. An existing battle
// is adopted only when it is that battle.
func (s *Service) start(ctx context.Context, store SchedulerStore, l *league.League, f league.Fixture) error {
	battleID := league.FixtureBattleID(l.BattleSalt, f.ID, f.Attempts+1)
	existing, err := store.Battle(ctx, battleID)
	if err != nil {
		return err
	}
	if existing != nil && !createdFor(l, f, *existing) {
		return failStart(ctx, store, l.ID, f, errForeignBattle)
	}
	if existing == nil {
		busy, err := store.EngineBusy(ctx)
		if err != nil || busy {
			return err
		}
		input := battleInput(battleID, f.ScenarioID, l.Settings.TickCount, l.Settings.TickDuration, l.Settings.DecisionIntervalTicks,
			l.Entrants[f.Home].FactionID, l.Entrants[f.Home].Controller, l.Entrants[f.Away].FactionID, l.Entrants[f.Away].Controller)
		input.DecisionBudgetExempt = true
		if _, err := s.battles.CreateBattle(ownerContext(ctx, l.OwnerID), input); err != nil {
			return failStart(ctx, store, l.ID, f, err)
		}
	}
	f.Attempts++
	f.Status, f.BattleID = league.FixtureRunning, &battleID
	slog.InfoContext(ctx, "league_fixture_started", "league_id", l.ID, "fixture_id", f.ID, "battle_id", battleID, "attempt", f.Attempts)
	return store.SaveFixture(ctx, f)
}

// createdFor reports whether an existing battle is the one the scheduler starts for the
// fixture: owned by the league owner, budget-exempt, on the fixture's map, home first.
func createdFor(l *league.League, f league.Fixture, b BattleProvenance) bool {
	return b.OwnerID == l.OwnerID && b.DecisionBudgetExempt && b.ScenarioID == f.ScenarioID && len(b.FactionIDs) == 2 &&
		b.FactionIDs[0] == l.Entrants[f.Home].FactionID && b.FactionIDs[1] == l.Entrants[f.Away].FactionID
}

func failStart(ctx context.Context, store SchedulerStore, leagueID string, f league.Fixture, err error) error {
	if !isAny(err, permanentStartErrors) {
		return err
	}
	f.Attempts++
	if f.Attempts >= league.MaxAttempts {
		f.Status = league.FixtureVoid
	}
	slog.WarnContext(ctx, "league_fixture_start_failed", "league_id", leagueID, "fixture_id", f.ID, "attempt", f.Attempts, "status", f.Status, "error", err)
	return store.SaveFixture(ctx, f)
}

func isAny(err error, targets []error) bool {
	for _, target := range targets {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// ownerContext acts as the league's owner. Only Admins create leagues, and battles they
// own skip the battle allowances and may lift the decision budget.
func ownerContext(ctx context.Context, ownerID string) context.Context {
	return authn.WithActor(ctx, authn.Actor{ID: ownerID, Role: authn.Admin})
}
