-- Records each Admin, Tester and Player who has signed in, with the role they last
-- signed in with and an optional display name that labels the battles and factions
-- they own. Guests and service identities are never recorded. A display name is 3-24
-- letters, digits, spaces, hyphens or underscores, with no leading or trailing space,
-- and unique ignoring case.

-- +goose Up
CREATE TABLE people (
 actor_id text PRIMARY KEY,
 display_name text CHECK (display_name ~ '^[A-Za-z0-9_-][A-Za-z0-9 _-]{1,22}[A-Za-z0-9_-]$'),
 role text NOT NULL CHECK (role IN ('admin','tester','player')),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX people_display_name_key ON people (lower(display_name));

-- +goose Down
DROP TABLE people;
