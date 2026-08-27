-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM factions) OR EXISTS (SELECT 1 FROM battle_runs) THEN
        RAISE EXCEPTION 'castle siege migration requires an empty development database';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE factions
    ADD COLUMN ruleset_id text NOT NULL,
    ADD COLUMN blueprint jsonb NOT NULL,
    ADD CONSTRAINT factions_ruleset_id_nonempty CHECK (length(trim(ruleset_id)) > 0),
    ADD CONSTRAINT factions_blueprint_object CHECK (jsonb_typeof(blueprint) = 'object');

ALTER TABLE battle_runs
    ADD COLUMN initial_state jsonb NOT NULL,
    ADD COLUMN current_state jsonb NOT NULL,
    ADD CONSTRAINT battle_initial_state_object CHECK (jsonb_typeof(initial_state) = 'object'),
    ADD CONSTRAINT battle_current_state_object CHECK (jsonb_typeof(current_state) = 'object');

-- +goose Down
ALTER TABLE battle_runs
    DROP CONSTRAINT battle_current_state_object,
    DROP CONSTRAINT battle_initial_state_object,
    DROP COLUMN current_state,
    DROP COLUMN initial_state;

ALTER TABLE factions
    DROP CONSTRAINT factions_blueprint_object,
    DROP CONSTRAINT factions_ruleset_id_nonempty,
    DROP COLUMN blueprint,
    DROP COLUMN ruleset_id;
