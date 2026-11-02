-- Gives each managed member a stored role, Player or Tester, and audits role changes.
-- Rows written before roles existed are Players. A role change needs no Cloudflare
-- synchronization: every active member belongs to the same managed Access group.

-- +goose Up
ALTER TABLE player_members ADD COLUMN role text NOT NULL DEFAULT 'player' CHECK (role IN ('player','tester'));
ALTER TABLE player_membership_audit
 ADD COLUMN role text CHECK (role IN ('player','tester')),
 DROP CONSTRAINT player_membership_audit_action_check,
 ADD CONSTRAINT player_membership_audit_action_check CHECK (action IN ('grant','remove','retry','import','role'));

-- +goose Down
-- Role-change audit rows have no earlier equivalent, and every member becomes a Player.
DELETE FROM player_membership_audit WHERE action='role';
ALTER TABLE player_membership_audit
 DROP CONSTRAINT player_membership_audit_action_check,
 ADD CONSTRAINT player_membership_audit_action_check CHECK (action IN ('grant','remove','retry','import')),
 DROP COLUMN role;
ALTER TABLE player_members DROP COLUMN role;
