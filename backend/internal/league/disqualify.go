// Removes entrants from a running league. A disqualified entrant forfeits the match it
// is playing and every match it reaches later, each to its opponent; results already
// decided stand. A match between two disqualified entrants goes to the higher seed, who
// forfeits its next match in turn. An entrant can be disqualified only while it can still
// win the league and another entrant who is not disqualified can too, so a disqualified
// entrant never becomes champion.

package league

import (
	"errors"
	"time"
)

// groupLossesOut is how many group losses end an entrant's league: a dual tournament
// is double elimination.
const groupLossesOut = 2

var (
	ErrEntrantNotFound     = errors.New("league entrant not found")
	ErrAlreadyDisqualified = errors.New("league entrant is already disqualified")
	ErrEntrantEliminated   = errors.New("league entrant has no match left to play")
	ErrLastContender       = errors.New("league entrant is the last one in contention")
)

// Disqualification records when an entrant was removed from its league, by whom, and
// the optional reason given.
type Disqualification struct {
	At     time.Time
	By     string
	Reason string
}

// Disqualify marks an entrant disqualified and forfeits its open fixtures: those pending,
// and those running whose battle has not completed. It returns the forfeited fixtures.
func (l *League) Disqualify(entrant int, d Disqualification) ([]Fixture, error) {
	if entrant < 0 || entrant >= len(l.Entrants) {
		return nil, ErrEntrantNotFound
	}
	if l.Disqualified(entrant) {
		return nil, ErrAlreadyDisqualified
	}
	eliminated := l.Eliminated()
	if eliminated[entrant] {
		return nil, ErrEntrantEliminated
	}
	if !l.otherContender(entrant, eliminated) {
		return nil, ErrLastContender
	}
	l.Entrants[entrant].Disqualified = &d
	forfeited := []Fixture{}
	for i, f := range l.Fixtures {
		if (f.Home == entrant || f.Away == entrant) && f.open() {
			l.Fixtures[i] = l.forfeit(f)
			forfeited = append(forfeited, l.Fixtures[i])
		}
	}
	return forfeited, nil
}

// Disqualified reports whether an entrant was disqualified.
func (l *League) Disqualified(entrant int) bool {
	return l.Entrants[entrant].Disqualified != nil
}

// Eliminated returns the entrants who can no longer win the league: two group losses,
// or one in the knockout.
func (l *League) Eliminated() map[int]bool {
	losses, eliminated := map[int]int{}, map[int]bool{}
	for _, f := range l.Fixtures {
		if l.MatchWinner(f) < 0 {
			continue
		}
		loser := l.decided(f).loser
		losses[loser]++
		if f.Stage != StageGroup || losses[loser] == groupLossesOut {
			eliminated[loser] = true
		}
	}
	return eliminated
}

// otherContender reports whether an entrant other than this one can still win and is
// not disqualified.
func (l *League) otherContender(entrant int, eliminated map[int]bool) bool {
	for _, e := range l.Entrants {
		if e.Index != entrant && !eliminated[e.Index] && e.Disqualified == nil {
			return true
		}
	}
	return false
}

// open reports whether a fixture can still be forfeited: it is pending, or running and
// its battle has not completed. A completed battle's result stands even before the
// scheduler records it.
func (f Fixture) open() bool {
	return f.Status == FixturePending || (f.Status == FixtureRunning && (f.Battle == nil || f.Battle.Status != BattleCompleted))
}

// forfeit settles a fixture against its disqualified side, or both sides.
func (l *League) forfeit(f Fixture) Fixture {
	home, away := l.Disqualified(f.Home), l.Disqualified(f.Away)
	f.Status, f.Forfeit = FixtureForfeited, pick(home && away, ForfeitBoth, pick(home, ForfeitHome, ForfeitAway))
	return f
}
