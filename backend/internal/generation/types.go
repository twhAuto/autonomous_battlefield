// Package generation handles account-scoped faction background drafts and quotas.
package generation

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/faction"
)

const JobType = "faction_background"

var (
	ErrNotFound = errors.New("generation not found")
	ErrLimited  = errors.New("generation allowance exhausted or another request is active")
	ErrConflict = errors.New("idempotency key already used for different input")
	ErrInvalid  = errors.New("invalid generation input")
)

// Config sets availability, the model profile and the per-account allowance.
type Config struct {
	Enabled          bool
	ProfileID        string
	DailyAllowance   int
	Cooldown         time.Duration
	QueueTimeout     time.Duration
	ExecutionTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{Enabled: true, ProfileID: "codex-subscription-local", DailyAllowance: 10, Cooldown: 30 * time.Second, QueueTimeout: 5 * time.Minute, ExecutionTimeout: 2 * time.Minute}
}
func (c Config) Validate() error {
	if strings.TrimSpace(c.ProfileID) == "" || c.DailyAllowance < 1 || c.Cooldown <= 0 || c.QueueTimeout <= 0 || c.ExecutionTimeout <= 0 {
		return errors.New("invalid faction background generation configuration")
	}
	return nil
}

type Input struct {
	Name       string `json:"name"`
	Doctrine   string `json:"doctrine"`
	Background string `json:"background"`
}

func (i Input) Normalized() (Input, error) {
	i.Name = strings.TrimSpace(i.Name)
	i.Doctrine = strings.TrimSpace(i.Doctrine)
	i.Background = strings.TrimSpace(i.Background)
	if i.Name == "" || i.Doctrine == "" || utf8.RuneCountInString(i.Doctrine) > faction.MaxDoctrineLength || utf8.RuneCountInString(i.Background) > faction.MaxBackgroundLength {
		return i, ErrInvalid
	}
	return i, nil
}

type Job struct {
	ID               string     `json:"id"`
	ActorID          string     `json:"-"`
	Input            Input      `json:"input"`
	ProfileID        string     `json:"profileId"`
	ModelLabel       string     `json:"modelLabel"`
	PromptVersion    string     `json:"promptVersion"`
	Status           string     `json:"status"`
	Background       string     `json:"background"`
	ErrorCategory    string     `json:"errorCategory,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	StartedAt        *time.Time `json:"startedAt,omitempty"`
	FinishedAt       *time.Time `json:"finishedAt,omitempty"`
	ExpiresAt        time.Time  `json:"expiresAt"`
	ExecutionSeconds int        `json:"executionTimeoutSeconds"`
}
type Options struct {
	Available   bool      `json:"available"`
	ModelLabel  string    `json:"modelLabel"`
	Reason      string    `json:"reason,omitempty"`
	Remaining   int       `json:"remaining"`
	ResetAt     time.Time `json:"resetAt"`
	RetryAt     time.Time `json:"retryAt"`
	ActiveJobID string    `json:"activeJobId,omitempty"`
}

// Admission names the model profile a queued job runs with.
type Admission struct {
	ProfileID  string
	ModelLabel string
	// Runtime is the profile's runtime, "api" or "cli"; CLI jobs share the CLI lease capacity.
	Runtime string
}

// Repository serves the API: allowance, admission and job reads, scoped to an actor.
type Repository interface {
	Options(ctx context.Context, actor string, quota Config) (Options, error)
	Admit(ctx context.Context, actor, idempotencyKey string, input Input, quota Config, model Admission) (Job, Options, error)
	Get(ctx context.Context, actor, id string) (Job, error)
}

// WorkerRepository serves the AI gateway's generation worker.
type WorkerRepository interface {
	Claim(context.Context, string, []string) (Job, error)
	Context(context.Context, string) (Job, error)
	Submit(context.Context, string, string) (bool, error)
	Finish(context.Context, string, string, ai.Usage, map[string]any) (bool, error)
}
