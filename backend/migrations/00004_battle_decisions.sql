-- +goose Up
CREATE TABLE battle_decisions (
    id uuid PRIMARY KEY,
    battle_id uuid NOT NULL REFERENCES battle_runs(id) ON DELETE CASCADE,
    attempt integer NOT NULL CHECK (attempt > 0),
    faction_id uuid NOT NULL,
    target_tick bigint NOT NULL CHECK (target_tick > 0),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    status text NOT NULL CHECK (status IN ('pending', 'running', 'accepted', 'fallback', 'failed')),
    controller_type text NOT NULL CHECK (controller_type IN ('scripted', 'model')),
    profile_id text NOT NULL DEFAULT '',
    provider text NOT NULL DEFAULT '',
    runtime text NOT NULL DEFAULT '',
    model_identifier text NOT NULL DEFAULT '',
    prompt_version text NOT NULL,
    tool_schema_version text NOT NULL,
    observation jsonb NOT NULL CHECK (jsonb_typeof(observation) = 'object'),
    observation_hash text NOT NULL,
    resolved_plan jsonb,
    fallback_source text NOT NULL DEFAULT '',
    error_category text NOT NULL DEFAULT '',
    usage jsonb NOT NULL DEFAULT '{}'::jsonb,
    provider_metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    idempotency_key text NOT NULL DEFAULT '',
    lease_owner text,
    lease_expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    UNIQUE (battle_id, attempt, faction_id, target_tick),
    CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL))
);

CREATE INDEX battle_decisions_agent_claim_idx
ON battle_decisions (status, lease_expires_at, created_at, id)
WHERE controller_type = 'model' AND status IN ('pending', 'running');

CREATE INDEX battle_decisions_window_idx
ON battle_decisions (battle_id, attempt, target_tick, faction_id);

CREATE VIEW battle_controller_evaluation AS
SELECT
    d.controller_type,
    CASE WHEN d.controller_type = 'scripted' THEN 'scripted' ELSE d.profile_id END AS profile_id,
    CASE WHEN d.controller_type = 'scripted' THEN 'scripted' ELSE d.provider END AS provider,
    CASE WHEN d.controller_type = 'scripted' THEN 'direct' ELSE d.runtime END AS runtime,
    count(*) AS decision_count,
    avg((d.status = 'fallback')::int) AS fallback_rate,
    avg((COALESCE((d.provider_metadata->>'validationFailures')::integer, 0) > 0)::int) AS invalid_plan_rate,
    avg(EXTRACT(epoch FROM (d.resolved_at - d.created_at)) * 1000) AS average_latency_ms,
    avg(COALESCE((d.usage->>'inputTokens')::bigint, 0)) AS average_input_tokens,
    avg(COALESCE((d.usage->>'outputTokens')::bigint, 0)) AS average_output_tokens,
    count(DISTINCT d.resolved_plan->>'name') AS production_diversity,
    avg((r.result->>'winnerFactionId' = d.faction_id::text)::int)
        FILTER (WHERE r.status = 'completed') AS win_rate
FROM battle_decisions d
JOIN battle_runs r ON r.id = d.battle_id
GROUP BY d.controller_type, d.profile_id, d.provider, d.runtime;

-- Operators can provision a LOGIN role and grant it battlefield_mcp. Keeping
-- the group role NOLOGIN separates credential lifecycle from schema lifecycle.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'battlefield_mcp') THEN
        CREATE ROLE battlefield_mcp NOLOGIN;
    END IF;
END
$$;
-- +goose StatementEnd
GRANT USAGE ON SCHEMA public TO battlefield_mcp;
GRANT SELECT, INSERT, UPDATE ON battle_decisions TO battlefield_mcp;
GRANT SELECT ON battle_controller_evaluation TO battlefield_mcp;

-- The scripted resolver and MCP decision service require no grants on
-- battle_runs, battle_events, units, buildings, or resource state.

-- +goose Down
DROP VIEW battle_controller_evaluation;
DROP TABLE battle_decisions;
-- The shared NOLOGIN role is intentionally retained because an operator may
-- have granted it to deployment-specific login roles.
