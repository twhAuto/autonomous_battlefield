// Seeds and advances the crossover knockout. Each group winner meets another group's
// runner-up, and the bracket keeps group-mates apart until the final.

package league

import "sort"

// Tie is one knockout pairing over two legs; Home takes the first leg's home side.
type Tie struct {
	Stage  string
	Number int
	Home   int
	Away   int
	Winner *int
}

// KnockoutStages lists the knockout stages, first to last, for a group count.
func KnockoutStages(groupCount int) []string {
	if groupCount == 4 {
		return []string{StageQuarterfinal, StageSemifinal, StageFinal}
	}
	return []string{StageSemifinal, StageFinal}
}

// TotalFixtures is the number of fixtures a league plays when nothing is cancelled.
func TotalFixtures(groupCount int) int {
	ties := 0
	for stage := range KnockoutStages(groupCount) {
		ties += 1 << (len(KnockoutStages(groupCount)) - stage - 1)
	}
	return groupCount*PairingsPerGroup*2 + ties*2
}

// Advance returns the next knockout stage's fixtures once every fixture so far is
// settled, or the champion once the final is. It returns neither while battles remain.
func (l *League) Advance() ([]Fixture, *int) {
	for _, f := range l.Fixtures {
		if !f.Settled() {
			return nil, nil
		}
	}
	stages := KnockoutStages(l.GroupCount)
	last := l.lastStage()
	if last == StageFinal {
		champion := l.TieWinner(StageFinal, 1)
		return nil, &champion
	}
	if last == StageGroup {
		return l.knockoutFixtures(stages[0], l.groupQualifiers()), nil
	}
	for i, stage := range stages[:len(stages)-1] {
		if stage == last {
			return l.knockoutFixtures(stages[i+1], l.winnerPairs(last)), nil
		}
	}
	return nil, nil
}

func (l *League) lastStage() string {
	last := StageGroup
	for _, f := range l.Fixtures {
		if f.Stage != StageGroup && stageRank(f.Stage) > stageRank(last) {
			last = f.Stage
		}
	}
	return last
}

func stageRank(stage string) int {
	return map[string]int{StageGroup: 0, StageQuarterfinal: 1, StageSemifinal: 2, StageFinal: 3}[stage]
}

// groupQualifiers pairs group winners with runners-up: A1-B2 and B1-A2, then C1-D2 and
// D1-C2. Winners of the first and third ties meet in a semifinal, so A1 and A2 can only
// meet in the final.
func (l *League) groupQualifiers() [][2]int {
	place := map[[2]int]int{}
	for entrant, standing := range l.AllStandings() {
		place[[2]int{standing.Group, standing.Position}] = entrant
	}
	pairs := [][2]int{}
	for group := 0; group < l.GroupCount; group += 2 {
		pairs = append(pairs, [2]int{place[[2]int{group, 1}], place[[2]int{group + 1, 2}]}, [2]int{place[[2]int{group + 1, 1}], place[[2]int{group, 2}]})
	}
	return pairs
}

// winnerPairs pairs the winners of a stage's ties: with four ties, 1 meets 3 and 2
// meets 4; with two, 1 meets 2.
func (l *League) winnerPairs(stage string) [][2]int {
	winners := []int{}
	for _, tie := range l.Ties() {
		if tie.Stage == stage {
			winners = append(winners, *tie.Winner)
		}
	}
	if len(winners) == 4 {
		return [][2]int{{winners[0], winners[2]}, {winners[1], winners[3]}}
	}
	return [][2]int{{winners[0], winners[1]}}
}

func (l *League) knockoutFixtures(stage string, pairs [][2]int) []Fixture {
	previousTies := len(l.Ties())
	fixtures := []Fixture{}
	for leg := 1; leg <= 2; leg++ {
		for i, pair := range pairs {
			home, away := pair[0], pair[1]
			if leg == 2 {
				home, away = away, home
			}
			scenario := l.Settings.ScenarioIDs[(l.GroupCount*PairingsPerGroup+previousTies+i)%len(l.Settings.ScenarioIDs)]
			fixtures = append(fixtures, newFixture(l.ID, len(l.Fixtures)+len(fixtures), Fixture{
				Stage: stage, Round: 1, Tie: i + 1, Leg: leg, Home: home, Away: away, ScenarioID: scenario,
			}))
		}
	}
	return fixtures
}

// Ties lists knockout ties in stage and tie order, with a winner once both legs settle.
func (l *League) Ties() []Tie {
	byKey := map[[2]any]*Tie{}
	settled := map[[2]any]bool{}
	ties := []*Tie{}
	for _, f := range l.Fixtures {
		if f.Stage == StageGroup {
			continue
		}
		key := [2]any{f.Stage, f.Tie}
		if byKey[key] == nil {
			byKey[key] = &Tie{Stage: f.Stage, Number: f.Tie}
			settled[key] = true
			ties = append(ties, byKey[key])
		}
		if f.Leg == 1 {
			byKey[key].Home, byKey[key].Away = f.Home, f.Away
		}
		settled[key] = settled[key] && f.Settled()
	}
	sort.SliceStable(ties, func(i, j int) bool {
		if ties[i].Stage != ties[j].Stage {
			return stageRank(ties[i].Stage) < stageRank(ties[j].Stage)
		}
		return ties[i].Number < ties[j].Number
	})
	result := make([]Tie, 0, len(ties))
	for _, tie := range ties {
		if settled[[2]any{tie.Stage, tie.Number}] {
			winner := l.TieWinner(tie.Stage, tie.Number)
			tie.Winner = &winner
		}
		result = append(result, *tie)
	}
	return result
}

// TieWinner decides a two-leg tie: more wins, then castle-health difference across both
// legs, then damage difference, then the better group finish, more group points and
// entry order. Void legs count for neither side.
func (l *League) TieWinner(stage string, number int) int {
	first, second, legs := l.tieLegs(stage, number)
	if first < 0 {
		return -1
	}
	wins, castle, damage := l.tally(first, legs)
	return l.tiebreak(first, second, wins, castle, damage)
}

// tieLegs returns a tie's legs and its sides, first being the first leg's home side.
func (l *League) tieLegs(stage string, number int) (int, int, []Fixture) {
	legs := []Fixture{}
	first, second := -1, -1
	for _, f := range l.Fixtures {
		if f.Stage != stage || f.Tie != number {
			continue
		}
		legs = append(legs, f)
		if f.Leg == 1 {
			first, second = f.Home, f.Away
		}
	}
	return first, second, legs
}

// tally sums a tie's scored legs from first's side: the win margin and the castle and
// damage differences.
func (l *League) tally(first int, legs []Fixture) (int, int64, int64) {
	var wins int
	var castle, damage int64
	for _, f := range legs {
		score, ok := l.Score(f)
		if !ok {
			continue
		}
		sign := int64(1)
		if f.Home != first {
			sign = -1
		}
		castle += sign * score.CastleDifference
		damage += sign * score.DamageDifference
		if score.Winner != nil {
			wins += pick(*score.Winner == first, 1, -1)
		}
	}
	return wins, castle, damage
}

func (l *League) tiebreak(first, second, wins int, castle, damage int64) int {
	for _, margin := range []int64{int64(wins), castle, damage} {
		if margin > 0 {
			return first
		}
		if margin < 0 {
			return second
		}
	}
	standings := l.AllStandings()
	a, b := standings[first], standings[second]
	if a.Position != b.Position {
		return pick(a.Position < b.Position, first, second)
	}
	if a.Points != b.Points {
		return pick(a.Points > b.Points, first, second)
	}
	return min(first, second)
}

func pick(condition bool, yes, no int) int {
	if condition {
		return yes
	}
	return no
}
