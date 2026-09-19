-- +goose Up
CREATE TABLE player_membership_sync (
 id boolean PRIMARY KEY DEFAULT true CHECK (id),
 initialized boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 0,
 synced_version bigint NOT NULL DEFAULT -1,
 attempts integer NOT NULL DEFAULT 0,
 next_attempt timestamptz NOT NULL DEFAULT now(),
 last_error text NOT NULL DEFAULT ''
);
INSERT INTO player_membership_sync(id) VALUES(true);
CREATE TABLE player_members (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 email text NOT NULL UNIQUE,
 desired_access boolean NOT NULL,
 version bigint NOT NULL DEFAULT 1,
 synced_version bigint NOT NULL DEFAULT 0,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE player_membership_audit (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 member_id uuid REFERENCES player_members(id),
 actor_id text NOT NULL,
 action text NOT NULL CHECK (action IN ('grant','remove','retry','import')),
 created_at timestamptz NOT NULL DEFAULT now()
);
-- +goose Down
DROP TABLE player_membership_audit;
DROP TABLE player_members;
DROP TABLE player_membership_sync;
