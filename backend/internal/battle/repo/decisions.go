// Persists decision windows, model claims, versioned submissions, fallbacks, usage, and
// faction-scoped history in PostgreSQL.

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/briefing"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
)

type decisionRow interface{ Scan(...any) error }

const decisionColumns = `id, battle_id, attempt, faction_id, target_tick, version, status,
controller_type, profile_id, provider, runtime, model_identifier, prompt_version, tool_schema_version,
observation, observation_hash, resolved_plan, fallback_source, error_category, usage, provider_metadata,
idempotency_key, traceparent, tracestate, created_at, resolved_at, lease_expires_at`

func (r *Postgres) CreateOrReuseDecision(ctx context.Context, params ai.CreateDecisionParams) (ai.DecisionRecord, error) {
	ctx, span := telemetry.StartDatabaseSpan(ctx, "create_decision")
	defer span.End()
	observation, err := json.Marshal(params.Observation)
	if err != nil {
		return ai.DecisionRecord{}, fmt.Errorf("marshal decision observation: %w", err)
	}
	var previousPlan []byte
	var previousSource string
	previousErr := r.db.QueryRow(ctx, `
		SELECT resolved_plan, CASE WHEN fallback_source = '' THEN 'retry_reuse' ELSE fallback_source END
		FROM battle_decisions
		WHERE battle_id = $1 AND faction_id = $2 AND target_tick = $3 AND observation_hash = $4
		  AND status IN ('accepted', 'fallback') AND resolved_plan IS NOT NULL
          AND prompt_version=$5 AND tool_schema_version=$6 AND profile_id=$7
          AND provider=$8 AND runtime=$9 AND model_identifier=$10
		ORDER BY attempt DESC LIMIT 1
	`, params.BattleID, params.FactionID, params.TargetTick, params.ObservationHash, params.PromptVersion, params.ToolSchemaVersion, params.ProfileID, params.Provider, params.Runtime, params.ModelIdentifier).Scan(&previousPlan, &previousSource)
	if previousErr != nil && !errors.Is(previousErr, pgx.ErrNoRows) {
		return ai.DecisionRecord{}, fmt.Errorf("find reusable decision: %w", previousErr)
	}
	status := "pending"
	var resolved any
	if previousErr == nil {
		status, resolved = "accepted", previousPlan
	}
	row := r.db.QueryRow(ctx, `
		WITH stored AS (
			INSERT INTO battle_decisions (id, battle_id, attempt, faction_id, target_tick, status, controller_type,
				profile_id, provider, runtime, model_identifier, prompt_version, tool_schema_version,
				observation, observation_hash, resolved_plan, fallback_source, traceparent, tracestate, resolved_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,
				CASE WHEN $16::jsonb IS NULL THEN NULL ELSE now() END)
			ON CONFLICT (battle_id, attempt, faction_id, target_tick) DO UPDATE SET battle_id = EXCLUDED.battle_id
			RETURNING *
		), notified AS (
			SELECT pg_notify('`+ai.DecisionReadyChannel+`', profile_id) AS sent
			FROM stored WHERE controller_type='model' AND status='pending'
		)
		SELECT `+decisionColumns+` FROM stored LEFT JOIN notified ON true`,
		params.ID, params.BattleID, params.Attempt, params.FactionID, params.TargetTick, status, params.ControllerType,
		params.ProfileID, params.Provider, params.Runtime, params.ModelIdentifier, params.PromptVersion,
		params.ToolSchemaVersion, observation, params.ObservationHash, resolved, previousSource,
		params.Traceparent, params.Tracestate)
	return scanDecision(row)
}

func (r *Postgres) GetDecision(ctx context.Context, id string) (ai.DecisionRecord, error) {
	record, err := scanDecision(r.db.QueryRow(ctx, `SELECT `+decisionColumns+` FROM battle_decisions WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ai.DecisionRecord{}, ai.ErrDecisionNotFound
	}
	return record, err
}

func (r *Postgres) ListWindowDecisions(ctx context.Context, battleID string, attempt int, targetTick uint64) ([]ai.DecisionRecord, error) {
	rows, err := r.db.Query(ctx, `SELECT `+decisionColumns+` FROM battle_decisions WHERE battle_id=$1 AND attempt=$2 AND target_tick=$3 ORDER BY faction_id`, battleID, attempt, targetTick)
	if err != nil {
		return nil, fmt.Errorf("list decision window: %w", err)
	}
	defer rows.Close()
	result := make([]ai.DecisionRecord, 0, 2)
	for rows.Next() {
		record, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Postgres) ListBattleDecisions(ctx context.Context, battleID string, attempt int) ([]ai.DecisionRecord, error) {
	rows, err := r.db.Query(ctx, `SELECT `+decisionColumns+` FROM battle_decisions WHERE battle_id=$1 AND attempt=$2 ORDER BY target_tick,faction_id`, battleID, attempt)
	if err != nil {
		return nil, fmt.Errorf("list battle decisions: %w", err)
	}
	defer rows.Close()
	result := make([]ai.DecisionRecord, 0)
	for rows.Next() {
		record, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func (r *Postgres) SubmitDecisionPlan(ctx context.Context, submission ai.PlanSubmission) (ai.SubmissionResult, error) {
	plan, err := json.Marshal(submission.Plan)
	if err != nil {
		return ai.SubmissionResult{}, err
	}
	var status string
	err = r.db.QueryRow(ctx, `
		UPDATE battle_decisions SET status='accepted', resolved_plan=$5, idempotency_key=$4,
			version=version+1, resolved_at=now(), lease_owner=NULL, lease_expires_at=NULL
		WHERE id=$1 AND version=$2 AND target_tick=$3 AND status IN ('pending','running')
		RETURNING status
	`, submission.RequestID, submission.ExpectedVersion, submission.TargetTick, submission.IdempotencyKey, plan).Scan(&status)
	if err == nil {
		return ai.SubmissionResult{Accepted: true, Status: status}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ai.SubmissionResult{}, fmt.Errorf("submit decision plan: %w", err)
	}
	var storedKey string
	err = r.db.QueryRow(ctx, `SELECT status, idempotency_key FROM battle_decisions WHERE id=$1`, submission.RequestID).Scan(&status, &storedKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return ai.SubmissionResult{}, ai.ErrDecisionNotFound
	}
	if err != nil {
		return ai.SubmissionResult{}, err
	}
	if storedKey == submission.IdempotencyKey && status == "accepted" {
		return ai.SubmissionResult{Accepted: true, Status: status}, nil
	}
	return ai.SubmissionResult{Status: status, Errors: []ai.ValidationIssue{{Field: "expectedVersion", Code: "conflict", Message: "decision was already resolved or changed"}}}, nil
}

func (r *Postgres) ResolveDecisionFallback(ctx context.Context, id string, expectedVersion int64, plan controller.PlanProposal, source, category string) (bool, error) {
	payload, err := json.Marshal(plan)
	if err != nil {
		return false, err
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE battle_decisions SET status='fallback', resolved_plan=$3, fallback_source=$4,
			error_category=$5, version=version+1, resolved_at=now(), lease_owner=NULL, lease_expires_at=NULL
		WHERE id=$1 AND version=$2 AND status IN ('pending','running','failed')
	`, id, expectedVersion, payload, source, category)
	if err != nil {
		return false, fmt.Errorf("resolve decision fallback: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Postgres) ClaimModelDecision(ctx context.Context, owner string, profileIDs []string, lease time.Duration) (ai.DecisionRecord, error) {
	ctx, span := telemetry.StartDatabaseSpan(ctx, "claim_decision")
	defer span.End()
	if len(profileIDs) == 0 {
		return ai.DecisionRecord{}, ai.ErrNoDecision
	}
	row := r.db.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id FROM battle_decisions
			WHERE controller_type='model' AND profile_id = ANY($3::text[])
			  AND (status='pending' OR (status='running' AND lease_expires_at <= now()))
			ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1
		), claimed AS (
			UPDATE battle_decisions d SET status='running', lease_owner=$1, lease_expires_at=now()+$2::interval
			FROM candidate WHERE d.id=candidate.id RETURNING d.*
		)
		SELECT `+decisionColumns+` FROM claimed`,
		owner, lease.String(), profileIDs)
	record, err := scanDecision(row)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && record.Status != "running") {
		return ai.DecisionRecord{}, ai.ErrNoDecision
	}
	return record, err
}

func (r *Postgres) FinishDecisionFailure(ctx context.Context, id, category string, usage ai.Usage, metadata map[string]any) error {
	usageJSON, _ := json.Marshal(usage)
	metadataJSON, _ := json.Marshal(metadata)
	_, err := r.db.Exec(ctx, `UPDATE battle_decisions SET status=CASE WHEN status='running' THEN 'failed' ELSE status END, error_category=$2, usage=$3,
		provider_metadata=provider_metadata || $4::jsonb, lease_owner=NULL, lease_expires_at=NULL WHERE id=$1 AND status IN ('running','accepted','fallback')`, id, category, usageJSON, metadataJSON)
	return err
}

func (r *Postgres) RecordDecisionUsage(ctx context.Context, id string, usage ai.Usage, metadata map[string]any) error {
	usageJSON, _ := json.Marshal(usage)
	metadataJSON, _ := json.Marshal(metadata)
	_, err := r.db.Exec(ctx, `UPDATE battle_decisions SET usage=$2, provider_metadata=provider_metadata || $3::jsonb WHERE id=$1 AND status IN ('accepted','fallback')`, id, usageJSON, metadataJSON)
	return err
}

func (r *Postgres) RecordDecisionValidation(ctx context.Context, id string) (int, error) {
	var failures int
	err := r.db.QueryRow(ctx, `UPDATE battle_decisions
		SET provider_metadata=jsonb_set(provider_metadata, '{validationFailures}',
			to_jsonb(COALESCE((provider_metadata->>'validationFailures')::integer, 0) + 1), true)
		WHERE id=$1 AND status IN ('pending','running')
		RETURNING (provider_metadata->>'validationFailures')::integer`, id).Scan(&failures)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return failures, err
}

func scanDecision(row decisionRow) (ai.DecisionRecord, error) {
	var record ai.DecisionRecord
	var observation, plan, usage, metadata []byte
	err := row.Scan(&record.ID, &record.BattleID, &record.Attempt, &record.FactionID, &record.TargetTick, &record.Version, &record.Status,
		&record.ControllerType, &record.ProfileID, &record.Provider, &record.Runtime, &record.ModelIdentifier,
		&record.PromptVersion, &record.ToolSchemaVersion, &observation, &record.ObservationHash, &plan,
		&record.FallbackSource, &record.ErrorCategory, &usage, &metadata, &record.IdempotencyKey,
		&record.Traceparent, &record.Tracestate,
		&record.CreatedAt, &record.ResolvedAt, &record.LeaseExpiresAt)
	if err != nil {
		return ai.DecisionRecord{}, err
	}
	if err := json.Unmarshal(observation, &record.Observation); err != nil {
		return ai.DecisionRecord{}, err
	}
	if len(plan) > 0 && string(plan) != "null" {
		var value controller.PlanProposal
		if err := json.Unmarshal(plan, &value); err != nil {
			return ai.DecisionRecord{}, err
		}
		record.ResolvedPlan = &value
	}
	if len(usage) > 0 {
		_ = json.Unmarshal(usage, &record.Usage)
	}
	if len(metadata) > 0 {
		_ = json.Unmarshal(metadata, &record.ProviderMetadata)
	}
	return record, nil
}

// ListFactionMemory filters before retrieval; another faction or attempt must
// never enter a model context, even if a downstream filter would discard it.
func (r *Postgres) ListFactionMemory(ctx context.Context, battleID string, attempt int, factionID string, beforeTick uint64) ([]ai.FactionMemoryEntry, error) {
	rows, err := r.db.Query(ctx, `SELECT `+decisionColumns+` FROM battle_decisions
        WHERE battle_id=$1 AND attempt=$2 AND faction_id=$3 AND target_tick<$4
          AND status IN ('accepted','fallback') AND resolved_plan IS NOT NULL
        ORDER BY target_tick DESC LIMIT $5`, battleID, attempt, factionID, beforeTick, briefing.FactionMemoryLimit)
	if err != nil {
		return nil, fmt.Errorf("list faction memory: %w", err)
	}
	defer rows.Close()
	var records []ai.DecisionRecord
	for rows.Next() {
		record, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return briefing.BuildFactionMemory(records, battleID, attempt, factionID, beforeTick), nil
}

func (r *Postgres) AgentEnabled(ctx context.Context) (bool, error) {
	var enabled bool
	err := r.db.QueryRow(ctx, `SELECT ai_enabled FROM runtime_policy WHERE id`).Scan(&enabled)
	return enabled, err
}
