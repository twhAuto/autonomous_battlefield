// Package generation handles account-scoped faction background drafts and quotas.
package generation

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

const JobType = "faction_background"

var (
	ErrNotFound = errors.New("generation not found")
	ErrLimited  = errors.New("generation allowance exhausted or another request is active")
	ErrConflict = errors.New("idempotency key already used for different input")
	ErrInvalid  = errors.New("invalid generation input")
)

type Config struct {
	Enabled          bool          `yaml:"enabled"`
	ProfileID        string        `yaml:"profileId"`
	DailyAllowance   int           `yaml:"dailyAllowance"`
	Cooldown         time.Duration `yaml:"-"`
	QueueTimeout     time.Duration `yaml:"-"`
	ExecutionTimeout time.Duration `yaml:"-"`
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
	if i.Name == "" || i.Doctrine == "" || utf8.RuneCountInString(i.Doctrine) > 4000 || utf8.RuneCountInString(i.Background) > 1000 {
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
type Repository interface {
	Options(context.Context, string, Config) (Options, error)
	Admit(context.Context, string, string, Input, Config, string) (Job, Options, error)
	Get(context.Context, string, string) (Job, error)
}
type WorkerRepository interface {
	Claim(context.Context, string, []string) (Job, error)
	Context(context.Context, string) (Job, error)
	Submit(context.Context, string, string) (bool, error)
	Finish(context.Context, string, string, ai.Usage, map[string]any) (bool, error)
}
