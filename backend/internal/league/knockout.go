// Advances the league: each group's next round, then the crossover knockout, where each
// group winner meets another group's runner-up and the bracket keeps group-mates apart
// until the final. Every knockout tie is one match.

package league

// Tie is one knockout match; Home takes the first spawn side.
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

// TotalFixtures is the number of fixtures a league plays when nothing is cancelled:
// five per group, then a bracket of half as many ties each stage down to the final.
func TotalFixtures(groupCount int) int {
	return groupCount*MatchesPerGroup + 1<<len(KnockoutStages(groupCount)) - 1
}

// Advance returns the fixtures that can open: each group's next round once that group's
// fixtures have settled, the first knockout stage once every group is placed, and each
// later stage once the previous one has settled. It returns the champion once the final
// has settled, and neither while battles remain.
func (l *League) Advance() ([]Fixture, *int) {
	stages := KnockoutStages(l.GroupCount)
	last := l.lastStage()
	if last == StageGroup {
		opened := []Fixture{}
		for group := range l.GroupCount {
			opened = append(opened, l.nextGroupRound(group, len(l.Fixtures)+len(opened))...)
		}
		if len(opened) > 0 || !l.groupsPlaced() {
			return opened, nil
		}
		return l.knockoutFixtures(stages[0], l.groupQualifiers()), nil
	}
	for _, f := range l.Fixtures {
		if !f.Settled() {
			return nil, nil
		}
	}
	if last == StageFinal {
		champion := l.MatchWinner(l.Fixtures[len(l.Fixtures)-1])
		return nil, &champion
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
		if stageRank(f.Stage) > stageRank(last) {
			last = f.Stage
		}
	}
	return last
}

func stageRank(stage string) int {
	return map[string]int{StageGroup: 0, StageQuarterfinal: 1, StageSemifinal: 2, StageFinal: 3}[stage]
}

// groupsPlaced reports whether every group has decided its decider.
func (l *League) groupsPlaced() bool {
	for group := range l.GroupCount {
		if l.MatchWinner(l.groupFixture(group, 3, 1)) < 0 {
			return false
		}
	}
	return true
}

// groupQualifiers pairs group winners with runners-up: A1-B2 and B1-A2, then C1-D2 and
// D1-C2. Winners of the first and third ties meet in a semifinal, so A1 and A2 can only
// meet in the final.
func (l *League) groupQualifiers() [][2]int {
	place := map[[2]int]int{}
	for group := range l.GroupCount {
		for _, standing := range l.GroupStandings(group) {
			place[[2]int{group, standing.Placement}] = standing.Entrant
		}
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

// knockoutFixtures opens a stage's ties in order. Knockout maps continue the group
// stage's rotation.
func (l *League) knockoutFixtures(stage string, pairs [][2]int) []Fixture {
	fixtures := []Fixture{}
	previous := l.GroupCount*MatchesPerGroup + len(l.Ties())
	for i, pair := range pairs {
		number := previous + i
		fixtures = append(fixtures, l.newFixture(len(l.Fixtures)+i, Fixture{
			Stage: stage, Round: 1, Tie: i + 1, ScenarioID: l.Settings.ScenarioIDs[number%len(l.Settings.ScenarioIDs)],
		}, pair[0], pair[1]))
	}
	return fixtures
}

// Ties lists knockout ties in stage and tie order, with a winner once decided.
func (l *League) Ties() []Tie {
	ties := []Tie{}
	for _, f := range l.Fixtures {
		if f.Stage == StageGroup {
			continue
		}
		tie := Tie{Stage: f.Stage, Number: f.Tie, Home: f.Home, Away: f.Away}
		if winner := l.MatchWinner(f); winner >= 0 {
			tie.Winner = &winner
		}
		ties = append(ties, tie)
	}
	return ties
}

func pick[T any](condition bool, yes, no T) T {
	if condition {
		return yes
	}
	return no
}
