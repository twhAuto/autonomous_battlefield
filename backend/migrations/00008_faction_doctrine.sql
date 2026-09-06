-- +goose Up
ALTER TABLE factions ADD COLUMN doctrine text NOT NULL DEFAULT 'Balance offense, defense, and economic growth. Protect your castle and income, scout before committing, and adapt purchases to visible threats.';
ALTER TABLE factions ADD CONSTRAINT factions_doctrine_valid CHECK (length(trim(doctrine)) BETWEEN 1 AND 4000);
ALTER TABLE factions ALTER COLUMN ruleset_id DROP NOT NULL, ALTER COLUMN blueprint DROP NOT NULL;
ALTER TABLE battle_decisions DROP CONSTRAINT battle_decisions_target_tick_check;
ALTER TABLE battle_decisions ADD CONSTRAINT battle_decisions_target_tick_check CHECK (target_tick >= 0);

-- +goose Down
-- Refuse a lossy downgrade when doctrine-only factions or opening records exist.
ALTER TABLE battle_decisions DROP CONSTRAINT battle_decisions_target_tick_check;
ALTER TABLE battle_decisions ADD CONSTRAINT battle_decisions_target_tick_check CHECK (target_tick > 0);
ALTER TABLE factions ALTER COLUMN ruleset_id SET NOT NULL, ALTER COLUMN blueprint SET NOT NULL;
ALTER TABLE factions DROP COLUMN doctrine;
