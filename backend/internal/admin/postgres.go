// Persists model-access policies and translates optimistic-write conflicts.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

type Postgres struct{ DB postgres.DBTX }

func (p *Postgres) Read(ctx context.Context) (Settings, error) {
	var s Settings
	var raw []byte
	err := p.DB.QueryRow(ctx, "SELECT version,policy FROM model_access_policy WHERE id").Scan(&s.Version, &raw)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(raw, &s.Policy)
	s.Policy = renameRole(s.Policy, legacyTesterRole, "tester")
	return s, err
}

// legacyTesterRole is how saved policies spell the tester role, so an image from
// before the rename reads them correctly after a rollback. Reads and saves translate
// it until a migration rewrites saved policies.
const legacyTesterRole = "developer"

// renameRole returns a copy of policy in which role from is spelled to.
func renameRole(policy profile.AccessPolicy, from, to string) profile.AccessPolicy {
	if policy.Profiles == nil {
		return policy
	}
	profiles := make(map[string]profile.Access, len(policy.Profiles))
	for id, access := range policy.Profiles {
		roles := slices.Clone(access.AllowedRoles)
		for i, role := range roles {
			if role == from {
				roles[i] = to
			}
		}
		access.AllowedRoles = roles
		profiles[id] = access
	}
	policy.Profiles = profiles
	return policy
}

func (p *Postgres) Save(ctx context.Context, s Settings, actorID string) (Settings, error) {
	raw, err := json.Marshal(renameRole(s.Policy, "tester", legacyTesterRole))
	if err != nil {
		return s, err
	}
	err = p.DB.QueryRow(ctx, "UPDATE model_access_policy SET policy=$1,version=version+1,updated_by=$2,updated_at=now() WHERE id AND version=$3 RETURNING version", raw, actorID, s.Version).Scan(&s.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrConflict
	}
	return s, err
}
