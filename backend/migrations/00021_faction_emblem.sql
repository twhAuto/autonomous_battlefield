-- Adds a saved cosmetic emblem with a star default for existing factions.
-- +goose Up
ALTER TABLE factions ADD COLUMN emblem text NOT NULL DEFAULT 'star'
    CHECK (emblem IN ('star', 'crown', 'shield', 'swords', 'wolf', 'raven',
                      'serpent', 'tree', 'leaf', 'flame', 'sun', 'moon'));

-- +goose Down
ALTER TABLE factions DROP COLUMN emblem;
