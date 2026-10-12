-- Renames the AI gateway's NOLOGIN group role from battlefield_mcp to
-- battlefield_ai_gateway. A rename keeps the role's OID, so existing grants and the
-- memberships of operator-provisioned login roles carry over without operator action.

-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'battlefield_ai_gateway') THEN
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'battlefield_mcp') THEN
            ALTER ROLE battlefield_mcp RENAME TO battlefield_ai_gateway;
        ELSE
            CREATE ROLE battlefield_ai_gateway NOLOGIN;
        END IF;
    END IF;
END
$$;
-- +goose StatementEnd

-- Roles are cluster-wide but grants are per schema. Restate the full grant set so the
-- role is complete here even when another database or schema on the same cluster
-- already performed the rename.
GRANT USAGE ON SCHEMA public TO battlefield_ai_gateway;
GRANT SELECT, INSERT, UPDATE ON battle_decisions TO battlefield_ai_gateway;
GRANT SELECT ON battle_controller_evaluation TO battlefield_ai_gateway;
GRANT SELECT, UPDATE ON faction_background_generations TO battlefield_ai_gateway;
GRANT SELECT, UPDATE(id) ON runtime_policy TO battlefield_ai_gateway;
GRANT SELECT(id, ai_executions), UPDATE(ai_executions) ON battle_runs TO battlefield_ai_gateway;
GRANT SELECT, INSERT, UPDATE, DELETE ON agent_execution_leases TO battlefield_ai_gateway;

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'battlefield_ai_gateway')
       AND NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'battlefield_mcp') THEN
        ALTER ROLE battlefield_ai_gateway RENAME TO battlefield_mcp;
    END IF;
END
$$;
-- +goose StatementEnd
