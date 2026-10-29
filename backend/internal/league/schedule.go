// Builds each group's dual tournament: two opening matches, then the winners' and
// losers' matches, then the decider, each round opening once the group's previous round
// has settled, independently of other groups.

package league

// GroupMembers returns a group's entrant indexes in entry order.
func GroupMembers(entrants []Entrant, group int) []int {
	members := []int{}
	for _, entrant := range entrants {
		if entrant.Group == group {
			members = append(members, entrant.Index)
		}
	}
	return members
}

// OpeningFixtures schedules every group's opening matches: within each group, by entry
// order, the first entrant meets the fourth and the second meets the third.
func (l *League) OpeningFixtures() []Fixture {
	fixtures := make([]Fixture, 0, l.GroupCount*2)
	for group := range l.GroupCount {
		members := GroupMembers(l.Entrants, group)
		for tie, pair := range [][2]int{{members[0], members[3]}, {members[1], members[2]}} {
			fixtures = append(fixtures, l.groupMatch(len(fixtures), group, 1, tie+1, pair))
		}
	}
	return fixtures
}

// nextGroupRound returns a group's next round once all its fixtures have settled: the
// winners' and losers' matches after the openings, then the decider between the
// winners'-match loser and the losers'-match winner. It returns nothing while the group
// has battles to play, and after the decider.
func (l *League) nextGroupRound(group, sequence int) []Fixture {
	played := l.groupFixtures(group)
	for _, f := range played {
		if !f.Settled() {
			return nil
		}
	}
	switch len(played) {
	case 2:
		first, second := l.decided(played[0]), l.decided(played[1])
		return []Fixture{
			l.groupMatch(sequence, group, 2, 1, [2]int{first.winner, second.winner}),
			l.groupMatch(sequence+1, group, 2, 2, [2]int{first.loser, second.loser}),
		}
	case 4:
		winners, losers := l.decided(l.groupFixture(group, 2, 1)), l.decided(l.groupFixture(group, 2, 2))
		return []Fixture{l.groupMatch(sequence, group, 3, 1, [2]int{winners.loser, losers.winner})}
	}
	return nil
}

// groupMatch builds a group fixture between pair, the earlier entrant first. Matches
// rotate through the maps in a fixed order: group by group, five matches each.
func (l *League) groupMatch(sequence, group, round, tie int, pair [2]int) Fixture {
	number := group*MatchesPerGroup + 2*(round-1) + tie - 1
	return l.newFixture(sequence, Fixture{Stage: StageGroup, Group: &group, Round: round, Tie: tie,
		ScenarioID: l.Settings.ScenarioIDs[number%len(l.Settings.ScenarioIDs)]}, min(pair[0], pair[1]), max(pair[0], pair[1]))
}

// newFixture completes a pending fixture between first and second, the higher seed
// first, with the coin flip choosing which takes the first spawn side.
func (l *League) newFixture(sequence int, f Fixture, first, second int) Fixture {
	if f.Group != nil {
		group := *f.Group
		f.Group = &group
	}
	f.ID, f.Sequence, f.Status = fixtureID(l.ID, sequence), sequence, FixturePending
	f.Home, f.Away = first, second
	if !homeFirst(l.BattleSalt, f.ID) {
		f.Home, f.Away = second, first
	}
	return f
}

func (l *League) groupFixtures(group int) []Fixture {
	fixtures := []Fixture{}
	for _, f := range l.Fixtures {
		if f.Stage == StageGroup && f.Group != nil && *f.Group == group {
			fixtures = append(fixtures, f)
		}
	}
	return fixtures
}

// groupFixture returns a group's fixture for a round and tie, or a zero fixture before
// that match opens.
func (l *League) groupFixture(group, round, tie int) Fixture {
	for _, f := range l.groupFixtures(group) {
		if f.Round == round && f.Tie == tie {
			return f
		}
	}
	return Fixture{}
}
