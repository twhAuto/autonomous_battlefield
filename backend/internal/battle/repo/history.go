// Queries filtered battle summaries with stable cursor ordering, keeping history listings
// separate from full battle reads.

package repo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	battleservice "github.com/twhAuto/autonomous_battlefield/backend/internal/battle/service"
)

// listBattlesSQL returns one history page. $1 status filter (empty for all), $2/$3
// cursor (NULL for the first page), $4 limit. listBattlesByVersionSQL adds $5, a rules
// version, in a separate statement: an "empty or equal" filter would not be an index
// condition in a generic plan, while an equality keeps the version and the cursor as
// conditions on battle_runs_rules_version_history_idx.
const (
	listBattlesSQL          = listBattlesSelect + listBattlesOrder
	listBattlesByVersionSQL = listBattlesSelect + `
   AND rules_version = $5` + listBattlesOrder
)

const listBattlesSelect = `
  SELECT id, scenario_id, COALESCE(rules_version, ''),
   COALESCE((SELECT jsonb_agg(jsonb_build_object('id', f->'id', 'name', f->'name', 'controller', f->'controller') ORDER BY ordinal)
     FROM jsonb_array_elements(scenario_snapshot->'factions') WITH ORDINALITY AS factions(f, ordinal)), '[]'::jsonb),
   status, paused, requested_ticks, current_tick, created_at, started_at, completed_at,
   CASE WHEN result IS NOT NULL AND result <> 'null'::jsonb THEN jsonb_build_object('winnerFactionId', result->'winnerFactionId', 'reason', result->'reason') END,
   owner_id
  FROM battle_runs
  WHERE ($1 = '' OR
    ($1 = 'paused' AND paused AND status IN ('queued', 'running')) OR
    ($1 IN ('queued', 'running') AND status = $1 AND NOT paused) OR
    ($1 IN ('completed', 'failed', 'stopped') AND status = $1))
   -- One row comparison with first-page sentinels stays an index condition even in
   -- the generic plans that cached prepared statements switch to; an "IS NULL OR"
   -- cursor becomes a filter there and scans every newer row.
   AND (created_at, id) < (COALESCE($2::timestamptz, 'infinity'), COALESCE($3::uuid, 'ffffffff-ffff-ffff-ffff-ffffffffffff'))`

const listBattlesOrder = `
  ORDER BY created_at DESC, id DESC
  LIMIT $4
`

func (r *Postgres) ListBattles(ctx context.Context, query battleservice.BattleHistoryQuery) ([]battleservice.BattleSummary, error) {
	var beforeTime, beforeID any
	if query.Before != nil {
		beforeTime = query.Before.CreatedAt
		beforeID = query.Before.ID
	}
	sql, args := listBattlesSQL, []any{query.Status, beforeTime, beforeID, query.Limit}
	if query.RulesVersion != "" {
		sql, args = listBattlesByVersionSQL, append(args, query.RulesVersion)
	}
	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list battles: %w", err)
	}
	defer rows.Close()
	battles := make([]battleservice.BattleSummary, 0)
	for rows.Next() {
		var battle battleservice.BattleSummary
		var participants, outcome []byte
		if err := rows.Scan(&battle.ID, &battle.ScenarioID, &battle.RulesVersion, &participants, &battle.Status, &battle.Paused, &battle.RequestedTicks, &battle.CurrentTick, &battle.CreatedAt, &battle.StartedAt, &battle.CompletedAt, &outcome, &battle.OwnerID); err != nil {
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

// listRulesVersionsSQL skips through battle_runs_rules_version_history_idx one distinct
// version at a time, reading a few index pages per version instead of every battle.
const listRulesVersionsSQL = `
  WITH RECURSIVE versions(version) AS (
    (SELECT rules_version FROM battle_runs WHERE rules_version IS NOT NULL ORDER BY rules_version LIMIT 1)
    UNION ALL
    SELECT (SELECT rules_version FROM battle_runs WHERE rules_version > versions.version ORDER BY rules_version LIMIT 1)
    FROM versions WHERE versions.version IS NOT NULL
  )
  SELECT version FROM versions WHERE version IS NOT NULL
`

// ListRulesVersions returns each distinct rules version recorded battles ran on.
func (r *Postgres) ListRulesVersions(ctx context.Context) ([]string, error) {
	rows, err := r.db.Query(ctx, listRulesVersionsSQL)
	if err != nil {
		return nil, fmt.Errorf("list rules versions: %w", err)
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("list rules versions: %w", err)
	}
	return versions, nil
}
