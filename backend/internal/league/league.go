// Package league holds the tournament rules for AI leagues: entrants in groups of four
// play a double round robin, the top two of each group meet in a crossover knockout,
// and every pairing is played over two legs, once from each spawn side. It is pure:
// storage, transport and battle creation live with its callers.
package league

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

const (
	GroupSize = 4
	// MaxAttempts is how many battles one fixture may start before it is void.
	MaxAttempts = 2

	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"

	StageGroup        = "group"
	StageQuarterfinal = "quarterfinal"
	StageSemifinal    = "semifinal"
	StageFinal        = "final"

	FixturePending   = "pending"
	FixtureRunning   = "running"
	FixtureCompleted = "completed"
	FixtureVoid      = "void"
	FixtureCancelled = "cancelled"

	PointsWin  = 3
	PointsDraw = 1

	// BattleCompleted is the battle status whose result a fixture scores.
	BattleCompleted = "completed"
)

type League struct {
	ID            string
	OwnerID       string
	Name          string
	Status        string
	RequestDigest string
	// BattleSalt keeps battle IDs unpredictable; it is never shown to clients.
	BattleSalt  string
	Settings    Settings
	GroupCount  int
	Entrants    []Entrant
	Fixtures    []Fixture
	Champion    *int
	CreatedAt   time.Time
	CompletedAt *time.Time
}

// Summary is a league in a list: no entrants or fixtures, only progress and champion.
type Summary struct {
	ID           string
	OwnerID      string
	Name         string
	Status       string
	GroupCount   int
	Champion     *int
	ChampionName *string
	Settled      int
	CreatedAt    time.Time
	CompletedAt  *time.Time
}

// Settings are the battle options every fixture of a league shares.
type Settings struct {
	ScenarioIDs           []string `json:"scenarioIds"`
	TickCount             uint64   `json:"tickCount"`
	TickDuration          *string  `json:"tickDuration,omitempty"`
	DecisionIntervalTicks *uint64  `json:"decisionIntervalTicks,omitempty"`
}

// Entrant is one competitor: a faction and the controller that plays it. Index is the
// entry order, the final tiebreak.
type Entrant struct {
	Index       int
	Group       int
	FactionID   string
	FactionName string
	Controller  game.ControllerConfig
}

// Fixture is one battle slot. Home takes the first spawn side.
type Fixture struct {
	ID         string
	Sequence   int
	Stage      string
	Group      *int
	Round      int
	Tie        int
	Leg        int
	Home       int
	Away       int
	ScenarioID string
	Status     string
	Attempts   int
	BattleID   *string
	Battle     *BattleOutcome
}

// BattleOutcome is the part of a fixture's current battle that the league scores.
type BattleOutcome struct {
	Status          string
	WinnerFactionID *string
	Reason          string
	Factions        map[string]FactionScore
}

type FactionScore struct {
	CastleHealth int64
	DamageDealt  int64
}

// Score is a completed fixture from the home entrant's side; Winner is nil for a draw.
type Score struct {
	Winner           *int
	CastleDifference int64
	DamageDifference int64
}

// GroupLetter names a group index for people: 0 is A.
func GroupLetter(group int) string { return string(rune('A' + group)) }

// Settled reports whether a fixture will produce no further battles.
func (f Fixture) Settled() bool {
	return f.Status == FixtureCompleted || f.Status == FixtureVoid || f.Status == FixtureCancelled
}

// Score returns a completed fixture's result. Void, cancelled and unfinished fixtures
// have none.
func (l *League) Score(f Fixture) (Score, bool) {
	if f.Status != FixtureCompleted || f.Battle == nil || f.Battle.Status != BattleCompleted {
		return Score{}, false
	}
	home, away := l.Entrants[f.Home], l.Entrants[f.Away]
	homeScore, awayScore := f.Battle.Factions[home.FactionID], f.Battle.Factions[away.FactionID]
	score := Score{CastleDifference: homeScore.CastleHealth - awayScore.CastleHealth, DamageDifference: homeScore.DamageDealt - awayScore.DamageDealt}
	if winner := f.Battle.WinnerFactionID; winner != nil {
		switch *winner {
		case home.FactionID:
			score.Winner = &f.Home
		case away.FactionID:
			score.Winner = &f.Away
		}
	}
	return score, true
}

// DerivedID builds a stable UUID from its parts, so retried writes reuse the same rows
// and battles instead of creating new ones.
func DerivedID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// FixtureBattleID is the battle a fixture starts on the given attempt, from 1. The
// league's secret salt stops anyone else from creating a battle with that ID first.
func FixtureBattleID(salt, fixtureID string, attempt int) string {
	return DerivedID("league-fixture-battle", salt, fixtureID, fmt.Sprint(attempt))
}

func fixtureID(leagueID string, sequence int) string {
	return DerivedID("league-fixture", leagueID, fmt.Sprint(sequence))
}
