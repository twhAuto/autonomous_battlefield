-- Lets membership audit history outlive a deleted member: deleting an expired removed
-- member clears its audit rows' member link instead of failing.

-- +goose Up
ALTER TABLE player_membership_audit
 DROP CONSTRAINT player_membership_audit_member_id_fkey,
 ADD CONSTRAINT player_membership_audit_member_id_fkey FOREIGN KEY (member_id) REFERENCES player_members(id) ON DELETE SET NULL;

-- +goose Down
-- Audit rows whose member was deleted already have a null member_id, which the earlier
-- constraint accepts.
ALTER TABLE player_membership_audit
 DROP CONSTRAINT player_membership_audit_member_id_fkey,
 ADD CONSTRAINT player_membership_audit_member_id_fkey FOREIGN KEY (member_id) REFERENCES player_members(id);
