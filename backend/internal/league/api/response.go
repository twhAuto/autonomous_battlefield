// Shapes a league for clients: settings, entrants with any disqualification, group
// records and placements, knockout ties and every fixture with its match, battle,
// progress while running, result, forfeit and winner. Entrants are referred to by index.

package api

import (
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/league"
)

type leagueJSON struct {
	ID         string          `json:"id"`
	OwnerID    string          `json:"ownerId"`
	Name       string          `json:"name"`
	Status     string          `json:"status"`
	GroupCount int             `json:"groupCount"`
	Settings   league.Settings `json:"settings"`
	Champion   *int            `json:"champion"`
	Progress   progressJSON    `json:"progress"`
	// RulesVersions lists the rules versions of the fixtures' battles, newest first.
	RulesVersions []string      `json:"rulesVersions"`
	Entrants      []entrantJSON `json:"entrants"`
	Groups        []groupJSON   `json:"groups"`
	Knockout      []tieJSON     `json:"knockout"`
	Fixtures      []fixtureJSON `json:"fixtures"`
	CreatedAt     time.Time     `json:"createdAt"`
	CompletedAt   *time.Time    `json:"completedAt"`
}

// entrantJSON is an entrant; Eliminated once it can no longer win, Disqualification null
// unless an admin disqualified it.
type entrantJSON struct {
	Index            int                   `json:"index"`
	Group            string                `json:"group"`
	FactionID        string                `json:"factionId"`
	FactionName      string                `json:"factionName"`
	Controller       game.ControllerConfig `json:"controller"`
	Eliminated       bool                  `json:"eliminated"`
	Disqualification *disqualificationJSON `json:"disqualification"`
}

type disqualificationJSON struct {
	At     time.Time `json:"at"`
	By     string    `json:"by"`
	Reason string    `json:"reason"`
}

type groupJSON struct {
	Group     string         `json:"group"`
	Standings []standingJSON `json:"standings"`
}

// standingJSON is an entrant's group record; Placement is null until decided.
type standingJSON struct {
	Entrant   int  `json:"entrant"`
	Placement *int `json:"placement"`
	Won       int  `json:"won"`
	Lost      int  `json:"lost"`
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
	Tie        int     `json:"tie"`
	Match      string  `json:"match,omitempty"`
	Home       int     `json:"homeEntrant"`
	Away       int     `json:"awayEntrant"`
	ScenarioID string  `json:"scenarioId"`
	Status     string  `json:"status"`
	Attempts   int     `json:"attempts"`
	BattleID   *string `json:"battleId"`
	// BattleProgress is present while the fixture runs; Result once it completed, with
	// Winner null for a draw. Forfeit names the disqualified side of a forfeited fixture.
	// Winner is the match winner once the fixture is completed, void or forfeited, after
	// the tiebreak where the battle had none.
	BattleProgress *battleProgressJSON `json:"battleProgress,omitempty"`
	Result         *resultJSON         `json:"result,omitempty"`
	Forfeit        string              `json:"forfeit,omitempty"`
	Winner         *int                `json:"winner"`
}

type battleProgressJSON struct {
	CurrentTick    int64 `json:"currentTick"`
	RequestedTicks int64 `json:"requestedTicks"`
}

type resultJSON struct {
	Winner *int   `json:"winner"`
	Reason string `json:"reason"`
}

func leagueResponse(l *league.League) leagueJSON {
	response := leagueJSON{
		ID: l.ID, OwnerID: l.OwnerID, Name: l.Name, Status: l.Status, GroupCount: l.GroupCount, Settings: l.Settings, Champion: l.Champion,
		Entrants: []entrantJSON{}, Groups: []groupJSON{}, Knockout: []tieJSON{}, Fixtures: []fixtureJSON{},
		Progress:      progressJSON{Total: league.TotalFixtures(l.GroupCount)},
		RulesVersions: l.RulesVersions(),
		CreatedAt:     l.CreatedAt, CompletedAt: l.CompletedAt,
	}
	eliminated := l.Eliminated()
	for _, e := range l.Entrants {
		item := entrantJSON{Index: e.Index, Group: league.GroupLetter(e.Group), FactionID: e.FactionID, FactionName: e.FactionName, Controller: e.Controller, Eliminated: eliminated[e.Index]}
		if d := e.Disqualified; d != nil {
			item.Disqualification = &disqualificationJSON{At: d.At, By: d.By, Reason: d.Reason}
		}
		response.Entrants = append(response.Entrants, item)
	}
	for group := range l.GroupCount {
		standings := []standingJSON{}
		for _, s := range l.GroupStandings(group) {
			item := standingJSON{Entrant: s.Entrant, Won: s.Won, Lost: s.Lost}
			if s.Placement > 0 {
				item.Placement = &s.Placement
			}
			standings = append(standings, item)
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
	item := fixtureJSON{ID: f.ID, Sequence: f.Sequence, Stage: f.Stage, Round: f.Round, Tie: f.Tie, Match: league.MatchName(f.Stage, f.Round, f.Tie),
		Home: f.Home, Away: f.Away, ScenarioID: f.ScenarioID, Status: f.Status, Attempts: f.Attempts, BattleID: f.BattleID, Forfeit: f.Forfeit}
	if f.Group != nil {
		letter := league.GroupLetter(*f.Group)
		item.Group = &letter
	}
	if f.Status == league.FixtureRunning && f.Battle != nil {
		item.BattleProgress = &battleProgressJSON{CurrentTick: f.Battle.CurrentTick, RequestedTicks: f.Battle.RequestedTicks}
	}
	if score, ok := l.Score(f); ok {
		item.Result = &resultJSON{Winner: score.Winner, Reason: f.Battle.Reason}
	}
	if winner := l.MatchWinner(f); winner >= 0 {
		item.Winner = &winner
	}
	return item
}
