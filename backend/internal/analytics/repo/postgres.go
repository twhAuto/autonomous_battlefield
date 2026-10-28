// Reads completed battles for analytics: each battle's frozen factions, controllers and
// result, plus its sides' model decision totals from the battle's final attempt.

package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/twh09/autonomous_battlefield/backend/internal/analytics"
	analyticsservice "github.com/twh09/autonomous_battlefield/backend/internal/analytics/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

type Postgres struct {
	db postgres.DBTX
}

func NewPostgres(db postgres.DBTX) *Postgres {
	return &Postgres{db: db}
}

func (r *Postgres) CompletedBattles(ctx context.Context, filter analyticsservice.Filter, limit int) ([]analytics.Battle, error) {
	rows, err := r.db.Query(ctx, `
		SELECT r.id, r.scenario_id, COALESCE(r.scenario_snapshot->>'rulesVersion', ''), r.scenario_snapshot->'factions', r.result
		FROM battle_runs r
		WHERE r.status = 'completed' AND r.result IS NOT NULL
		  AND ($1 = '' OR r.scenario_snapshot->>'rulesVersion' = $1)
		  AND ($2::timestamptz IS NULL OR r.completed_at >= $2)
		  AND ($3 = '' OR EXISTS (SELECT 1 FROM league_fixtures f WHERE f.battle_id = r.id AND f.league_id::text = $3))
		ORDER BY r.completed_at DESC, r.id DESC
		LIMIT $4`, filter.RulesVersion, filter.Since, filter.LeagueID, limit)
	if err != nil {
		return nil, fmt.Errorf("list completed battles: %w", err)
	}
	battles, err := pgx.CollectRows(rows, scanBattle)
	if err != nil {
		return nil, fmt.Errorf("list completed battles: %w", err)
	}
	if err := r.addDecisions(ctx, battles); err != nil {
		return nil, err
	}
	return battles, nil
}

type snapshotFaction struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Controller game.ControllerConfig `json:"controller"`
}

func scanBattle(row pgx.CollectableRow) (analytics.Battle, error) {
	var b analytics.Battle
	var factions, result []byte
	if err := row.Scan(&b.ID, &b.ScenarioID, &b.RulesVersion, &factions, &result); err != nil {
		return b, err
	}
	var sides []snapshotFaction
	if err := json.Unmarshal(factions, &sides); err != nil || len(sides) != 2 {
		return b, fmt.Errorf("decode battle %q factions: %v", b.ID, err)
	}
	var decoded game.Result
	if err := json.Unmarshal(result, &decoded); err != nil {
		return b, fmt.Errorf("decode battle %q result: %w", b.ID, err)
	}
	b.Reason, b.WinnerFactionID, b.TicksRun = decoded.Reason, decoded.WinnerFactionID, decoded.TicksRun
	for i, faction := range sides {
		b.Sides[i] = analytics.Side{FactionID: faction.ID, FactionName: faction.Name, Controller: faction.Controller}
		for j := range decoded.Factions {
			if decoded.Factions[j].FactionID == faction.ID {
				b.Sides[i].Stats = &decoded.Factions[j]
			}
		}
	}
	return b, nil
}

// addDecisions attaches each side's model decision totals from the final attempt. A
// retried battle stores a plan reused from an earlier attempt as accepted with its
// original fallback source, so reused fallbacks still count as fallbacks.
func (r *Postgres) addDecisions(ctx context.Context, battles []analytics.Battle) error {
	if len(battles) == 0 {
		return nil
	}
	index := make(map[string]*analytics.Battle, len(battles))
	ids := make([]string, 0, len(battles))
	for i := range battles {
		index[battles[i].ID] = &battles[i]
		ids = append(ids, battles[i].ID)
	}
	rows, err := r.db.Query(ctx, `
		SELECT d.battle_id::text, d.faction_id::text,
		       count(*),
		       count(*) FILTER (WHERE d.status IN ('fallback', 'failed') OR d.fallback_source IN ('scripted_fallback', 'previous_plan')),
		       count(*) FILTER (WHERE COALESCE((d.provider_metadata->>'validationFailures')::integer, 0) > 0),
		       count(*) FILTER (WHERE d.resolved_at IS NOT NULL),
		       COALESCE(sum(EXTRACT(epoch FROM d.resolved_at - d.created_at) * 1000) FILTER (WHERE d.resolved_at IS NOT NULL), 0)::float8,
		       COALESCE(sum((d.usage->>'inputTokens')::bigint), 0)::bigint,
		       COALESCE(sum((d.usage->>'cachedInputTokens')::bigint), 0)::bigint,
		       COALESCE(sum((d.usage->>'outputTokens')::bigint), 0)::bigint
		FROM battle_decisions d
		JOIN battle_runs r ON r.id = d.battle_id AND d.attempt = r.attempt
		WHERE d.battle_id = ANY($1::uuid[]) AND d.controller_type = 'model'
		GROUP BY d.battle_id, d.faction_id`, ids)
	if err != nil {
		return fmt.Errorf("total battle decisions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var battleID, factionID string
		var d analytics.Decisions
		if err := rows.Scan(&battleID, &factionID, &d.Model, &d.Fallbacks, &d.InvalidPlans, &d.Resolved, &d.LatencyMS, &d.InputTokens, &d.CachedInputTokens, &d.OutputTokens); err != nil {
			return fmt.Errorf("scan battle decisions: %w", err)
		}
		if b := index[battleID]; b != nil {
			for i := range b.Sides {
				if strings.EqualFold(b.Sides[i].FactionID, factionID) {
					b.Sides[i].Decisions = d
				}
			}
		}
	}
	return rows.Err()
}
