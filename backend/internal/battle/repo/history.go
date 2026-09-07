package repo

import (
	"context"
	"encoding/json"
	"fmt"

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
)

func (r *Postgres) ListBattles(ctx context.Context, query battleservice.BattleHistoryQuery) ([]battleservice.BattleSummary, error) {
	var beforeTime, beforeID any
	if query.Before != nil {
		beforeTime = query.Before.CreatedAt
		beforeID = query.Before.ID
	}
	rows, err := r.db.Query(ctx, `
  SELECT id, scenario_id,
   COALESCE((SELECT jsonb_agg(jsonb_build_object('id', f->'id', 'name', f->'name', 'controller', f->'controller') ORDER BY ordinal)
     FROM jsonb_array_elements(scenario_snapshot->'factions') WITH ORDINALITY AS factions(f, ordinal)), '[]'::jsonb),
   status, paused, requested_ticks, current_tick, created_at, started_at, completed_at,
   CASE WHEN result IS NOT NULL AND result <> 'null'::jsonb THEN jsonb_build_object('winnerFactionId', result->'winnerFactionId', 'reason', result->'reason') END
  FROM battle_runs
  WHERE ($1 = '' OR
    ($1 = 'paused' AND paused AND status IN ('queued', 'running')) OR
    ($1 IN ('queued', 'running') AND status = $1 AND NOT paused) OR
    ($1 IN ('completed', 'failed', 'stopped') AND status = $1))
   AND ($2::timestamptz IS NULL OR (created_at, id) < ($2::timestamptz, $3::uuid))
  ORDER BY created_at DESC, id DESC
  LIMIT $4
 `, query.Status, beforeTime, beforeID, query.Limit)
	if err != nil {
		return nil, fmt.Errorf("list battles: %w", err)
	}
	defer rows.Close()
	battles := make([]battleservice.BattleSummary, 0)
	for rows.Next() {
		var battle battleservice.BattleSummary
		var participants, outcome []byte
		if err := rows.Scan(&battle.ID, &battle.ScenarioID, &participants, &battle.Status, &battle.Paused, &battle.RequestedTicks, &battle.CurrentTick, &battle.CreatedAt, &battle.StartedAt, &battle.CompletedAt, &outcome); err != nil {
			return nil, fmt.Errorf("scan battle summary: %w", err)
		}
		if err := json.Unmarshal(participants, &battle.Participants); err != nil {
			return nil, fmt.Errorf("decode battle participants: %w", err)
		}
		if len(outcome) > 0 {
			if err := json.Unmarshal(outcome, &battle.Outcome); err != nil {
				return nil, fmt.Errorf("decode battle outcome: %w", err)
			}
		}
		battles = append(battles, battle)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate battle summaries: %w", err)
	}
	return battles, nil
}
