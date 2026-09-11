-- +goose Up
CREATE TABLE model_access_policy (
 id boolean PRIMARY KEY DEFAULT true CHECK (id),
 version bigint NOT NULL DEFAULT 0,
 policy jsonb NOT NULL DEFAULT '{"providers":{},"profiles":{}}',
 updated_by text NOT NULL DEFAULT '',
 updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO model_access_policy(id) VALUES(true);

-- +goose Down
DROP TABLE model_access_policy;
