package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/worker"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"time"
)

func (r *Postgres) SaveTick(ctx context.Context, lease worker.BattleLease, transition game.Transition, leaseDuration time.Duration) error {
	events, err := json.Marshal(transition.Events)
	if err != nil {
		return fmt.Errorf("marshal tick events: %w", err)
	}
	state, err := json.Marshal(transition.State)
	if err != nil {
		return fmt.Errorf("marshal tick state: %w", err)
	}
	var owned bool
	err = r.db.QueryRow(ctx, `
		WITH owned AS (
			UPDATE battle_runs
			SET current_tick = $4, current_state = $5, lease_expires_at = now() + $6::interval, updated_at = now()
			WHERE id = $1 AND status = 'running' AND attempt = $2 AND lease_owner = $3
			RETURNING id
		), inserted AS (
			INSERT INTO battle_events (battle_id, attempt, sequence, tick, type, payload)
			SELECT $1, $2, event.sequence, event.tick, event.type, event.data
			FROM owned, jsonb_to_recordset($7::jsonb)
			     AS event(sequence bigint, tick bigint, type text, data jsonb)
			ON CONFLICT (battle_id, attempt, sequence) DO NOTHING
		)
		SELECT EXISTS (SELECT 1 FROM owned)
	`, lease.BattleID, lease.Attempt, lease.Owner, transition.Tick, state, leaseDuration.String(), events).Scan(&owned)
	if err != nil {
		return fmt.Errorf("save battle %q tick %d: %w", lease.BattleID, transition.Tick, err)
	}
	if !owned {
		return worker.ErrBattleLeaseLost
	}
	return nil
}

func (r *Postgres) CompleteBattle(ctx context.Context, lease worker.BattleLease, transition game.Transition) error {
	if !transition.Finished || transition.Result == nil {
		return errors.New("cannot complete battle without a finished transition")
	}
	events, err := json.Marshal(transition.Events)
	if err != nil {
		return fmt.Errorf("marshal completion events: %w", err)
	}
	result, err := json.Marshal(transition.Result)
	if err != nil {
		return fmt.Errorf("marshal battle result: %w", err)
	}
	state, err := json.Marshal(transition.State)
	if err != nil {
		return fmt.Errorf("marshal final state: %w", err)
	}
	var owned bool
	err = r.db.QueryRow(ctx, `
		WITH owned AS (
			UPDATE battle_runs
			SET status = 'completed', paused = false, current_tick = $4, result = $5, current_state = $6,
			    lease_owner = NULL, lease_expires_at = NULL,
			    completed_at = now(), updated_at = now()
			WHERE id = $1 AND status = 'running' AND attempt = $2 AND lease_owner = $3
			RETURNING id
		), inserted AS (
			INSERT INTO battle_events (battle_id, attempt, sequence, tick, type, payload)
			SELECT $1, $2, event.sequence, event.tick, event.type, event.data
			FROM owned, jsonb_to_recordset($7::jsonb)
			     AS event(sequence bigint, tick bigint, type text, data jsonb)
			ON CONFLICT (battle_id, attempt, sequence) DO NOTHING
		)
		SELECT EXISTS (SELECT 1 FROM owned)
	`, lease.BattleID, lease.Attempt, lease.Owner, transition.Tick, result, state, events).Scan(&owned)
	if err != nil {
		return fmt.Errorf("complete battle %q: %w", lease.BattleID, err)
	}
	if !owned {
		return worker.ErrBattleLeaseLost
	}
	return nil
}
