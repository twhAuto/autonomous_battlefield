// Builds analytics reports for Testers and Admins from completed battles, applying
// the league, rules-version, date and fallback filters.

package service

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/analytics"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
)

var ErrInvalidQuery = errors.New("invalid analytics query")

const (
	// MaxBattles bounds one report to the most recently completed battles.
	MaxBattles = 5000
	// AllRulesVersions selects battles of every rules version.
	AllRulesVersions = "all"
)

var uuidPattern = regexp.MustCompile(`^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$`)

// Query selects the battles a report covers. An empty RulesVersion means the current
// rules; AllRulesVersions means every version.
type Query struct {
	LeagueID        string
	RulesVersion    string
	Since           *time.Time
	ExcludeFallback bool
}

// Filter is the storage-side selection; empty fields select everything.
type Filter struct {
	LeagueID     string
	RulesVersion string
	Since        *time.Time
}

// Repository returns completed battles, newest first, at most limit of them.
type Repository interface {
	CompletedBattles(context.Context, Filter, int) ([]analytics.Battle, error)
}

type Service struct {
	repo         Repository
	currentRules string
}

// New reports on currentRules by default; an empty version disables the default filter.
func New(repo Repository, currentRules string) *Service {
	return &Service{repo: repo, currentRules: currentRules}
}

func (s *Service) Report(ctx context.Context, q Query) (analytics.Report, error) {
	if actor, ok := authn.ActorFromContext(ctx); !ok || !actor.CanView() {
		return analytics.Report{}, authn.ErrForbidden
	}
	if q.LeagueID != "" && !uuidPattern.MatchString(q.LeagueID) {
		return analytics.Report{}, ErrInvalidQuery
	}
	rules := q.RulesVersion
	if rules == "" {
		rules = s.currentRules
	}
	if rules == AllRulesVersions {
		rules = ""
	}
	battles, err := s.repo.CompletedBattles(ctx, Filter{LeagueID: strings.ToLower(q.LeagueID), RulesVersion: rules, Since: q.Since}, MaxBattles+1)
	if err != nil {
		return analytics.Report{}, err
	}
	truncated := len(battles) > MaxBattles
	if truncated {
		battles = battles[:MaxBattles]
	}
	kept := battles
	if q.ExcludeFallback {
		kept = make([]analytics.Battle, 0, len(battles))
		for _, b := range battles {
			if !b.HasFallback() {
				kept = append(kept, b)
			}
		}
	}
	report := analytics.Aggregate(kept)
	report.Truncated, report.ExcludedForFallback = truncated, len(battles)-len(kept)
	if rules != "" {
		report.RulesVersion = &rules
	}
	return report, nil
}
