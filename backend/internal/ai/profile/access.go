package profile

import (
	"context"
	"slices"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
)

// AccessPolicy restricts new selections; deployment configuration still determines
// which models can run. Existing battle controller snapshots are unchanged.
type AccessPolicy struct {
	Providers map[string]bool   `json:"providers"`
	Profiles  map[string]Access `json:"profiles"`
}
type Access struct {
	Enabled      bool     `json:"enabled"`
	AllowedRoles []string `json:"allowedRoles"`
	// Models narrows individual model options to some of the profile's roles. A model
	// without an entry allows every role the profile allows.
	Models map[string]ModelAccess `json:"models,omitempty"`
}
type ModelAccess struct {
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
		p.ModelRoles = modelRoles(p, rule.Models)
	}
	if !enabled {
		p.Available = false
		p.UnavailableReason = "disabled by administrator"
	}
	return p
}

// modelRoles keeps the policy entries of configured model options. A fresh map leaves
// the shared configuration untouched; an entry's empty list denies everyone.
func modelRoles(p ai.ModelProfile, models map[string]ModelAccess) map[string][]string {
	if len(models) == 0 {
		return nil
	}
	result := make(map[string][]string, len(models))
	for _, option := range p.ModelOptions {
		if access, ok := models[option.ID]; ok {
			result[option.ID] = access.AllowedRoles
		}
	}
	return result
}

// ModelAllowed reports whether the caller may select model on p: the profile must allow
// the caller's role, and so must the model's own policy entry when it has one. A model
// that is not a configured option has no entry; selection still rejects it.
func ModelAllowed(ctx context.Context, p ai.ModelProfile, model string) bool {
	if !Allowed(ctx, p) {
		return false
	}
	roles, restricted := p.ModelRoles[model]
	if !restricted {
		return true
	}
	a, _ := authn.ActorFromContext(ctx)
	return slices.Contains(roles, string(a.Role))
}

// ForRole narrows p to what the caller may select: the model options the caller's role
// may use, with the configured model replaced by the first of them when the role may
// not use it. It reports false when the caller may use no model of p.
func ForRole(ctx context.Context, p ai.ModelProfile) (ai.ModelProfile, bool) {
	if !Allowed(ctx, p) {
		return p, false
	}
	var options []ai.ModelOption
	for _, option := range p.ModelOptions {
		if ModelAllowed(ctx, p, option.ID) {
			options = append(options, option)
		}
	}
	if !ModelAllowed(ctx, p, ai.ConfiguredModel(p)) {
		if len(options) == 0 {
			return p, false
		}
		// Only CLI profiles have model options, and a CLI profile labels its model by ID.
		p.Model, p.ModelLabel = options[0].ID, options[0].ID
	}
	p.ModelOptions = options
	return p, true
}
