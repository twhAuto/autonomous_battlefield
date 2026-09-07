-- +goose Up
ALTER TABLE factions ADD COLUMN background text NOT NULL DEFAULT '' CHECK (char_length(background) <= 1000);
CREATE TABLE faction_generation_accounts (
 actor_id text PRIMARY KEY,
 last_admitted_at timestamptz
);
CREATE TABLE faction_background_generations (
 id uuid PRIMARY KEY,
 actor_id text NOT NULL REFERENCES faction_generation_accounts(actor_id),
 idempotency_key text NOT NULL,
 input_hash text NOT NULL,
 input jsonb NOT NULL,
 profile_id text NOT NULL,
 model_label text NOT NULL,
 prompt_version text NOT NULL,
 status text NOT NULL CHECK (status IN ('queued','running','succeeded','failed','expired')),
 background text NOT NULL DEFAULT '' CHECK (char_length(background) <= 1000),
 error_category text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 started_at timestamptz,
 finished_at timestamptz,
 expires_at timestamptz NOT NULL,
 execution_seconds integer NOT NULL CHECK (execution_seconds > 0),
 worker_id text NOT NULL DEFAULT '',
 usage jsonb NOT NULL DEFAULT '{}',
 metadata jsonb NOT NULL DEFAULT '{}',
 submissions integer NOT NULL DEFAULT 0,
 UNIQUE(actor_id,idempotency_key)
);
CREATE UNIQUE INDEX faction_generation_one_active ON faction_background_generations(actor_id) WHERE status IN ('queued','running');
CREATE INDEX faction_generation_quota ON faction_background_generations(actor_id,created_at);
CREATE INDEX faction_generation_queue ON faction_background_generations(created_at) WHERE status='queued';

-- Generation workers can access drafts, but receive no faction or battle-state grants.
GRANT SELECT, UPDATE ON faction_background_generations TO battlefield_mcp;

-- +goose Down
DROP TABLE faction_background_generations;
DROP TABLE faction_generation_accounts;
ALTER TABLE factions DROP COLUMN background;
