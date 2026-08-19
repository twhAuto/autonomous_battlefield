-- +goose Up
CREATE TABLE factions (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (length(trim(name)) > 0),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE factions;
