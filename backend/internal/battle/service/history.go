// Validates history filters and cursors and constructs paginated battle summaries from
// repository results.

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
)

type BattleParticipantSummary struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Controller game.ControllerConfig `json:"controller"`
}
type BattleOutcome struct {
	WinnerFactionID *string `json:"winnerFactionId"`
	Reason          string  `json:"reason"`
}
type BattleSummary struct {
	// OwnerID selects OwnerName, the owner's label, which the API adds to each row.
	OwnerID        string                     `json:"-"`
	OwnerName      string                     `json:"ownerName,omitempty"`
	ID             string                     `json:"id"`
	ScenarioID     string                     `json:"scenarioId"`
	RulesVersion   string                     `json:"rulesVersion"`
	Participants   []BattleParticipantSummary `json:"participants"`
	Status         string                     `json:"status"`
	Paused         bool                       `json:"paused"`
	RequestedTicks uint64                     `json:"requestedTicks"`
	CurrentTick    uint64                     `json:"currentTick"`
	CreatedAt      time.Time                  `json:"createdAt"`
	StartedAt      *time.Time                 `json:"startedAt"`
	CompletedAt    *time.Time                 `json:"completedAt"`
	Outcome        *BattleOutcome             `json:"outcome"`
}
type BattleHistoryPage struct {
	Battles    []BattleSummary `json:"battles"`
	NextCursor *string         `json:"nextCursor"`
}

// BattleHistoryInput selects a history page; an empty Status or RulesVersion matches
// every battle.
type BattleHistoryInput struct {
	Status       string
	RulesVersion string
	Cursor       string
	Limit        int
}
type BattleHistoryCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}
type BattleHistoryQuery struct {
	Status       string
	RulesVersion string
	Before       *BattleHistoryCursor
	Limit        int
}

// RulesVersions lists the rules versions battles have recorded, newest first, with
// Current, the version new battles use, always among them.
type RulesVersions struct {
	Current  string   `json:"current"`
	Versions []string `json:"versions"`
}

// ListBattles validates the filter and cursor, then fetches one extra summary to
// decide whether to return a next-page cursor without a separate count query.
func (s *battleService) ListBattles(ctx context.Context, input BattleHistoryInput) (BattleHistoryPage, error) {
	page := BattleHistoryPage{Battles: []BattleSummary{}}
	switch input.Status {
	case "", "queued", "running", "paused", "completed", "failed", "stopped":
	default:
		return page, ErrInvalidBattle
	}
	if input.Limit < 1 || input.Limit > 100 || len(input.RulesVersion) > game.MaxRulesVersionLength {
		return page, ErrInvalidBattle
	}
	query := BattleHistoryQuery{Status: input.Status, RulesVersion: input.RulesVersion, Limit: input.Limit + 1}
	if input.Cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		if err != nil || len(data) > 256 {
			return page, ErrInvalidBattle
		}
		var cursor BattleHistoryCursor
		if json.Unmarshal(data, &cursor) != nil || cursor.CreatedAt.IsZero() || !battleUUIDPattern.MatchString(cursor.ID) {
			return page, ErrInvalidBattle
		}
		query.Before = &cursor
	}
	battles, err := s.battles.ListBattles(ctx, query)
	if err != nil {
		return page, err
	}
	if len(battles) > input.Limit {
		last := battles[input.Limit-1]
		data, _ := json.Marshal(BattleHistoryCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		cursor := base64.RawURLEncoding.EncodeToString(data)
		page.NextCursor = &cursor
		battles = battles[:input.Limit]
	}
	if battles != nil {
		page.Battles = battles
	}
	return page, nil
}

// ListRulesVersions lists the recorded rules versions together with the current one,
// newest first.
func (s *battleService) ListRulesVersions(ctx context.Context) (RulesVersions, error) {
	versions, err := s.battles.ListRulesVersions(ctx)
	if err != nil {
		return RulesVersions{}, err
	}
	result := RulesVersions{Versions: versions}
	if summaries := s.catalog.Summaries(); len(summaries) > 0 {
		result.Current = summaries[0].RulesVersion
		if !slices.Contains(result.Versions, result.Current) {
			result.Versions = append(result.Versions, result.Current)
		}
	}
	if result.Versions == nil {
		result.Versions = []string{}
	}
	slices.SortFunc(result.Versions, game.CompareRulesVersions)
	return result, nil
}
