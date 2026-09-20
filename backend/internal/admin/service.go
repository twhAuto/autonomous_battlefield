// Owns model-access defaults, validation, and policy reads independently of HTTP.
package admin

import (
	"context"
	"errors"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

var (
	ErrInvalid  = errors.New("invalid model access settings")
	ErrConflict = errors.New("model access settings changed")
)

type Settings struct {
	Version int64           `json:"version"`
	Policy  ai.AccessPolicy `json:"policy"`
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
	Profiles   *ai.ProfileRegistry
}

// ReadPolicy supplies the registry callback, including its bounded database read.
func (s *Service) ReadPolicy() (ai.AccessPolicy, error) {
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
	settings.Policy = ai.AccessPolicy{Providers: map[string]bool{}, Profiles: map[string]ai.ProfileAccess{}}
	for _, p := range models {
		if value, ok := stored.Providers[p.Provider]; ok {
			settings.Policy.Providers[p.Provider] = value
		} else {
			settings.Policy.Providers[p.Provider] = true
		}
		if value, ok := stored.Profiles[p.ID]; ok {
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

func defaultProfileAccess(p ai.ModelProfile) ai.ProfileAccess {
	roles := p.AllowedRoles
	if len(roles) == 0 {
		roles = []string{"admin", "developer"}
	}
	filtered := []string{}
	for _, role := range roles {
		if p.Runtime != "cli" || role != "player" {
			filtered = append(filtered, role)
		}
	}
	return ai.ProfileAccess{Enabled: true, AllowedRoles: filtered}
}
func validRoles(roles []string, runtime string) bool {
	if roles == nil {
		return false
	}
	seen := map[string]bool{}
	for _, role := range roles {
		if (role != "admin" && role != "developer" && role != "player") || (role == "player" && runtime == "cli") || seen[role] {
			return false
		}
		seen[role] = true
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
		if !ok || !validRoles(rule.AllowedRoles, p.Runtime) {
			return false
		}
		if _, ok := settings.Policy.Providers[p.Provider]; !ok {
			return false
		}
	}
	return len(settings.Policy.Providers) == len(providers)
}
