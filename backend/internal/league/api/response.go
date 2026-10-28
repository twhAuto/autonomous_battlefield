// Shapes a league for clients: settings, entrants, ranked group tables, knockout ties and
// every fixture with its battle and result. Entrants are referred to by index.

package api

import (
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/league"
)

type leagueJSON struct {
	ID          string          `json:"id"`
	OwnerID     string          `json:"ownerId"`
	Name        string          `json:"name"`
	Status      string          `json:"status"`
	GroupCount  int             `json:"groupCount"`
	Settings    league.Settings `json:"settings"`
	Champion    *int            `json:"champion"`
	Progress    progressJSON    `json:"progress"`
	Entrants    []entrantJSON   `json:"entrants"`
	Groups      []groupJSON     `json:"groups"`
	Knockout    []tieJSON       `json:"knockout"`
	Fixtures    []fixtureJSON   `json:"fixtures"`
	CreatedAt   time.Time       `json:"createdAt"`
	CompletedAt *time.Time      `json:"completedAt"`
}

type entrantJSON struct {
	Index       int                   `json:"index"`
	Group       string                `json:"group"`
	FactionID   string                `json:"factionId"`
	FactionName string                `json:"factionName"`
	Controller  game.ControllerConfig `json:"controller"`
}

type groupJSON struct {
	Group     string         `json:"group"`
	Standings []standingJSON `json:"standings"`
}

type standingJSON struct {
	Entrant          int   `json:"entrant"`
	Position         int   `json:"position"`
	Played           int   `json:"played"`
	Won              int   `json:"won"`
	Drawn            int   `json:"drawn"`
	Lost             int   `json:"lost"`
	Points           int   `json:"points"`
	CastleDifference int64 `json:"castleDifference"`
	DamageDifference int64 `json:"damageDifference"`
}

type tieJSON struct {
	Stage  string `json:"stage"`
	Tie    int    `json:"tie"`
	Home   int    `json:"homeEntrant"`
	Away   int    `json:"awayEntrant"`
	Winner *int   `json:"winner"`
}

type fixtureJSON struct {
	ID         string  `json:"id"`
	Sequence   int     `json:"sequence"`
	Stage      string  `json:"stage"`
	Group      *string `json:"group,omitempty"`
	Round      int     `json:"round"`
	Tie        int     `json:"tie,omitempty"`
	Leg        int     `json:"leg"`
	Home       int     `json:"homeEntrant"`
	Away       int     `json:"awayEntrant"`
	ScenarioID string  `json:"scenarioId"`
	Status     string  `json:"status"`
	Attempts   int     `json:"attempts"`
	BattleID   *string `json:"battleId"`
	// Result is present once the fixture completed; Winner is null for a draw.
	Result *resultJSON `json:"result,omitempty"`
}

type resultJSON struct {
	Winner *int   `json:"winner"`
	Reason string `json:"reason"`
}

func leagueResponse(l *league.League) leagueJSON {
	response := leagueJSON{
		ID: l.ID, OwnerID: l.OwnerID, Name: l.Name, Status: l.Status, GroupCount: l.GroupCount, Settings: l.Settings, Champion: l.Champion,
		Entrants: []entrantJSON{}, Groups: []groupJSON{}, Knockout: []tieJSON{}, Fixtures: []fixtureJSON{},
		Progress:  progressJSON{Total: league.TotalFixtures(l.GroupCount)},
		CreatedAt: l.CreatedAt, CompletedAt: l.CompletedAt,
	}
	for _, e := range l.Entrants {
		response.Entrants = append(response.Entrants, entrantJSON{Index: e.Index, Group: league.GroupLetter(e.Group), FactionID: e.FactionID, FactionName: e.FactionName, Controller: e.Controller})
	}
	for group := range l.GroupCount {
		standings := []standingJSON{}
		for _, s := range l.GroupStandings(group) {
			standings = append(standings, standingJSON{Entrant: s.Entrant, Position: s.Position, Played: s.Played, Won: s.Won, Drawn: s.Drawn, Lost: s.Lost, Points: s.Points, CastleDifference: s.CastleDifference, DamageDifference: s.DamageDifference})
		}
		response.Groups = append(response.Groups, groupJSON{Group: league.GroupLetter(group), Standings: standings})
	}
	for _, tie := range l.Ties() {
		response.Knockout = append(response.Knockout, tieJSON{Stage: tie.Stage, Tie: tie.Number, Home: tie.Home, Away: tie.Away, Winner: tie.Winner})
	}
	for _, f := range l.Fixtures {
		if f.Settled() {
			response.Progress.Settled++
		}
		response.Fixtures = append(response.Fixtures, fixtureResponse(l, f))
	}
	return response
}

func fixtureResponse(l *league.League, f league.Fixture) fixtureJSON {
	item := fixtureJSON{ID: f.ID, Sequence: f.Sequence, Stage: f.Stage, Round: f.Round, Tie: f.Tie, Leg: f.Leg, Home: f.Home, Away: f.Away,
		ScenarioID: f.ScenarioID, Status: f.Status, Attempts: f.Attempts, BattleID: f.BattleID}
	if f.Group != nil {
		letter := league.GroupLetter(*f.Group)
		item.Group = &letter
	}
	if score, ok := l.Score(f); ok {
		item.Result = &resultJSON{Winner: score.Winner, Reason: f.Battle.Reason}
	}
	return item
}
