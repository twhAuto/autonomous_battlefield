package profile

import "github.com/twh09/autonomous_battlefield/backend/internal/ai"

// AccessPolicy restricts new selections; deployment configuration still determines
// which models can run. Existing battle controller snapshots are unchanged.
type AccessPolicy struct {
	Providers map[string]bool   `json:"providers"`
	Profiles  map[string]Access `json:"profiles"`
}
type Access struct {
	Enabled      bool     `json:"enabled"`
	AllowedRoles []string `json:"allowedRoles"`
}

// WithAccessPolicy returns a registry sharing immutable configuration and reading
// durable policy on each selection. It must be wired before serving requests.
func (r *Registry) WithAccessPolicy(read func() (AccessPolicy, error)) *Registry {
	return &Registry{profiles: r.profiles, accessPolicy: read}
}
func applyAccess(p ai.ModelProfile, policy AccessPolicy, err error) ai.ModelProfile {
	if err != nil {
		p.Available = false
		p.UnavailableReason = "model access policy could not be loaded"
		return p
	}
	enabled := true
	if value, ok := policy.Providers[p.Provider]; ok {
		enabled = value
	}
	if rule, ok := policy.Profiles[p.ID]; ok {
		enabled = enabled && rule.Enabled
		p.AllowedRoles = rule.AllowedRoles
		// Empty configuration roles mean defaults; an explicitly empty policy means no access.
		if len(p.AllowedRoles) == 0 {
			p.AllowedRoles = []string{"none"}
		}
	}
	if !enabled {
		p.Available = false
		p.UnavailableReason = "disabled by administrator"
	}
	return p
}
