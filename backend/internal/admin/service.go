// Owns model-access defaults, validation, and policy reads independently of HTTP.
package admin

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
)

var (
	ErrInvalid  = errors.New("invalid model access settings")
	ErrConflict = errors.New("model access settings changed")
)

type Settings struct {
	Version int64                `json:"version"`
	Policy  profile.AccessPolicy `json:"policy"`
}

type SettingsView struct {
	Settings
	Models []ai.ModelProfile `json:"models"`
}

// Repository persists the policy with optimistic version checking.
type Repository interface {
	Read(context.Context) (Settings, error)
	Save(context.Context, Settings, string) (Settings, error)
}

type Service struct {
	Repository Repository
	Profiles   *profile.Registry
}

// ReadPolicy supplies the registry callback, including its bounded database read.
func (s *Service) ReadPolicy() (profile.AccessPolicy, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	settings, err := s.Repository.Read(ctx)
	return settings.Policy, err
}

func (s *Service) Get(ctx context.Context) (SettingsView, error) {
	settings, err := s.Repository.Read(ctx)
	if err != nil {
		return SettingsView{}, err
	}
	models := s.Profiles.List()
	stored := settings.Policy
	settings.Policy = profile.AccessPolicy{Providers: map[string]bool{}, Profiles: map[string]profile.Access{}}
	for _, p := range models {
		if value, ok := stored.Providers[p.Provider]; ok {
			settings.Policy.Providers[p.Provider] = value
		} else {
			settings.Policy.Providers[p.Provider] = true
		}
		if value, ok := stored.Profiles[p.ID]; ok {
			value.Models = configuredModels(p, value.Models)
			settings.Policy.Profiles[p.ID] = value
		} else {
			settings.Policy.Profiles[p.ID] = defaultProfileAccess(p)
		}
	}
	return SettingsView{Settings: settings, Models: models}, nil
}

func (s *Service) Save(ctx context.Context, settings Settings, actorID string) (Settings, error) {
	if !s.valid(settings) {
		return Settings{}, ErrInvalid
	}
	return s.Repository.Save(ctx, settings, actorID)
}

// configuredModels drops entries for models the profile does not offer and moves an
// entry saved under an alias to its option, so a saved policy keeps validating and
// applying after a configuration change.
func configuredModels(p ai.ModelProfile, models map[string]profile.ModelAccess) map[string]profile.ModelAccess {
	return profile.OptionModels(p, models)
}

func defaultProfileAccess(p ai.ModelProfile) profile.Access {
	roles := p.AllowedRoles
	if len(roles) == 0 {
		roles = []string{"admin", "tester"}
	}
	return profile.Access{Enabled: true, AllowedRoles: roles}
}
func validRoles(roles []string) bool {
	if roles == nil {
		return false
	}
	seen := map[string]bool{}
	for _, role := range roles {
		if (role != "admin" && role != "tester" && role != "player") || seen[role] {
			return false
		}
		seen[role] = true
	}
	return true
}

// validModels accepts entries only for the profile's model options, each with roles
// the profile itself allows.
func validModels(p ai.ModelProfile, rule profile.Access) bool {
	for id, access := range rule.Models {
		offered := slices.ContainsFunc(p.ModelOptions, func(option ai.ModelOption) bool { return option.ID == id })
		if !offered || !validRoles(access.AllowedRoles) {
			return false
		}
		for _, role := range access.AllowedRoles {
			if !slices.Contains(rule.AllowedRoles, role) {
				return false
			}
		}
	}
	return true
}
func (s *Service) valid(settings Settings) bool {
	models := s.Profiles.List()
	providers := map[string]bool{}
	if settings.Version < 0 || len(settings.Policy.Profiles) != len(models) {
		return false
	}
	for _, p := range models {
		providers[p.Provider] = true
		rule, ok := settings.Policy.Profiles[p.ID]
		if !ok || !validRoles(rule.AllowedRoles) || !validModels(p, rule) {
			return false
		}
		if _, ok := settings.Policy.Providers[p.Provider]; !ok {
			return false
		}
	}
	return len(settings.Policy.Providers) == len(providers)
}
