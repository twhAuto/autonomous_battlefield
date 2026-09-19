// Evaluates configured model profiles for catalog visibility or local execution, checking
// credentials and CLI availability.

package ai

import (
	"context"
	"errors"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/clienv"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrProfileUnavailable = errors.New("model profile unavailable")

type ProfileRegistry struct {
	profiles     map[string]ModelProfile
	accessPolicy func() (AccessPolicy, error)
}

type profileRegistryPurpose uint8

const (
	profileExecutor profileRegistryPurpose = iota
	profileCatalog
	claudeCLIProvider      = "claude-cli"
	codexCLIProvider       = "codex-cli"
	antigravityCLIProvider = "antigravity-cli"
)

func NewProfileRegistry(environment string, configured []ModelProfile) *ProfileRegistry {
	return newProfileRegistry(environment, configured, profileExecutor)
}

// NewProfileCatalog evaluates profiles for battle selection. In a cluster it
// may advertise an operator-enabled local CLI profile supplied by an external
// host worker, without claiming that the API pod itself can execute the CLI.
func NewProfileCatalog(environment string, configured []ModelProfile) *ProfileRegistry {
	return newProfileRegistry(environment, configured, profileCatalog)
}

func newProfileRegistry(environment string, configured []ModelProfile, purpose profileRegistryPurpose) *ProfileRegistry {
	registry := &ProfileRegistry{profiles: make(map[string]ModelProfile, len(configured))}
	for _, raw := range configured {
		profile := evaluateProfile(strings.TrimSpace(environment), raw, purpose)
		registry.profiles[profile.ID] = profile
	}
	return registry
}

func (r *ProfileRegistry) Get(id string) (ModelProfile, bool) {
	profile, ok := r.profiles[id]
	if ok && r.accessPolicy != nil {
		policy, err := r.accessPolicy()
		profile = applyAccess(profile, policy, err)
	}
	return profile, ok
}

func (r *ProfileRegistry) Require(id string) (ModelProfile, error) {
	profile, ok := r.Get(id)
	if !ok || !profile.Available {
		return ModelProfile{}, ErrProfileUnavailable
	}
	return profile, nil
}

func (r *ProfileRegistry) List() []ModelProfile {
	result := make([]ModelProfile, 0, len(r.profiles))
	var policy AccessPolicy
	var err error
	if r.accessPolicy != nil {
		policy, err = r.accessPolicy()
	}
	for _, profile := range r.profiles {
		if r.accessPolicy != nil {
			profile = applyAccess(profile, policy, err)
		}
		result = append(result, profile)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// AvailableIDs returns the allowlisted profiles this process can execute. Agent
// workers send this list when claiming work so heterogeneous API and CLI
// workers cannot lease each other's decisions.
func (r *ProfileRegistry) AvailableIDs() []string {
	profiles := r.List()
	result := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		if profile.Available {
			result = append(result, profile.ID)
		}
	}
	return result
}

func evaluateProfile(environment string, profile ModelProfile, purpose profileRegistryPurpose) ModelProfile {
	profile.ID = strings.TrimSpace(profile.ID)
	profile.Model = strings.TrimSpace(profile.Model)
	if profile.Runtime == "cli" {
		profile.ModelLabel = profile.Model
	}
	profile.ToolCapability = true
	if !profile.Enabled {
		profile.UnavailableReason = "disabled by configuration"
		return profile
	}
	if profile.ID == "" || profile.DisplayName == "" || (profile.Runtime != "api" && profile.Runtime != "cli") || (profile.Scope != "local" && profile.Scope != "cluster") {
		profile.UnavailableReason = "invalid profile configuration"
		return profile
	}
	if environment == "cluster" && profile.Scope == "local" {
		return evaluateExternalCLIProfile(profile, purpose)
	}
	if profile.Runtime == "api" {
		return evaluateAPIProfile(environment, profile)
	}
	return evaluateLocalCLIProfile(profile)
}

func evaluateExternalCLIProfile(profile ModelProfile, purpose profileRegistryPurpose) ModelProfile {
	if purpose != profileCatalog || !strings.EqualFold(strings.TrimSpace(os.Getenv(profile.AvailabilityEnvironmentVariable)), "true") {
		profile.UnavailableReason = "host CLI agent is not connected"
		return profile
	}
	if profile.Runtime != "cli" || (profile.Provider != codexCLIProvider && profile.Provider != antigravityCLIProvider && profile.Provider != claudeCLIProvider) || strings.TrimSpace(profile.SupportedVersionPrefix) == "" {
		profile.UnavailableReason = "invalid external CLI profile configuration"
		return profile
	}
	if profile.ModelLabel == "" {
		profile.ModelLabel = strings.TrimSuffix(profile.Provider, "-cli") + " subscription (host)"
	}
	profile.Available = true
	return profile
}

func evaluateAPIProfile(environment string, profile ModelProfile) ModelProfile {
	if profile.Provider != "openai" && profile.Provider != "gemini" {
		profile.UnavailableReason = "unsupported API provider"
		return profile
	}
	credential := strings.TrimSpace(os.Getenv(profile.CredentialEnvironmentVariable))
	model := strings.TrimSpace(os.Getenv(profile.ModelEnvironmentVariable))
	profile.ModelLabel = model
	if environment == "cluster" && profile.AvailabilityEnvironmentVariable != "" && !strings.EqualFold(strings.TrimSpace(os.Getenv(profile.AvailabilityEnvironmentVariable)), "true") {
		profile.UnavailableReason = "provider credentials are not configured"
		return profile
	}
	if environment != "cluster" && (credential == "" || model == "") {
		profile.UnavailableReason = "provider credentials or model are not configured"
		return profile
	}
	if environment == "cluster" && profile.ModelLabel == "" {
		profile.ModelLabel = "operator configured"
	}
	profile.Available = true
	return profile
}

func evaluateLocalCLIProfile(profile ModelProfile) ModelProfile {
	binary := map[string]string{claudeCLIProvider: "claude", codexCLIProvider: "codex", antigravityCLIProvider: "agy"}[profile.Provider]
	if binary == "" {
		profile.UnavailableReason = "unsupported CLI provider"
		return profile
	}
	if _, err := exec.LookPath(binary); err != nil {
		profile.UnavailableReason = binary + " CLI is not installed"
		return profile
	}
	version, err := exec.Command(binary, "--version").Output()
	if err != nil || profile.SupportedVersionPrefix == "" || !strings.HasPrefix(strings.TrimSpace(string(version)), profile.SupportedVersionPrefix) {
		profile.UnavailableReason = binary + " CLI version is not supported"
		return profile
	}
	if profile.Provider == antigravityCLIProvider {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := clienv.CheckAntigravityAuth(ctx); err != nil {
			profile.UnavailableReason = err.Error()
			return profile
		}
	} else if profile.Provider == claudeCLIProvider {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := clienv.CheckClaudeAuth(ctx); err != nil {
			profile.UnavailableReason = err.Error()
			return profile
		}
	} else {
		home, _ := os.UserHomeDir()
		if _, err := os.Stat(filepath.Join(home, ".codex", "auth.json")); err != nil {
			profile.UnavailableReason = binary + " CLI is not authenticated"
			return profile
		}
	}
	if profile.ModelLabel == "" {
		profile.ModelLabel = binary + " subscription"
	}
	profile.Available = true
	return profile
}

// ProfileAllowed is also enforced on submitted IDs, not just catalog visibility.
func ProfileAllowed(ctx context.Context, p ModelProfile) bool {
	a, ok := authn.ActorFromContext(ctx)
	if !ok {
		return false
	}
	if a.Role == authn.Player && p.Runtime == "cli" {
		return false
	}
	if len(p.AllowedRoles) == 0 {
		return a.CanInspect()
	}
	for _, role := range p.AllowedRoles {
		if role == string(a.Role) {
			return true
		}
	}
	return false
}
