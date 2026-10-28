// Builds the group-stage schedule: within each group of four, every pairing plays once
// from each spawn side, round by round so all groups progress together.

package league

// roundRobin lists each round's pairings by position within a group of four, home
// first. The second leg repeats them with sides swapped.
var roundRobin = [3][2][2]int{
	{{0, 3}, {1, 2}},
	{{0, 2}, {3, 1}},
	{{0, 1}, {2, 3}},
}

// PairingsPerGroup is the number of distinct pairings in a group of four.
const PairingsPerGroup = 6

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

// GroupFixtures schedules the group stage. Each pairing keeps one map for both legs,
// and pairings rotate through scenarioIDs in schedule order.
func GroupFixtures(leagueID string, entrants []Entrant, groupCount int, scenarioIDs []string) []Fixture {
	fixtures := make([]Fixture, 0, groupCount*PairingsPerGroup*2)
	for leg := 1; leg <= 2; leg++ {
		for roundIndex, round := range roundRobin {
			for group := range groupCount {
				members := GroupMembers(entrants, group)
				for match, pair := range round {
					home, away := members[pair[0]], members[pair[1]]
					if leg == 2 {
						home, away = away, home
					}
					pairing := group*PairingsPerGroup + roundIndex*len(round) + match
					fixtures = append(fixtures, newFixture(leagueID, len(fixtures), Fixture{
						Stage: StageGroup, Group: &group, Round: (leg-1)*len(roundRobin) + roundIndex + 1, Leg: leg,
						Home: home, Away: away, ScenarioID: scenarioIDs[pairing%len(scenarioIDs)],
					}))
				}
			}
		}
	}
	return fixtures
}

func newFixture(leagueID string, sequence int, f Fixture) Fixture {
	if f.Group != nil {
		group := *f.Group
		f.Group = &group
	}
	f.ID, f.Sequence, f.Status = fixtureID(leagueID, sequence), sequence, FixturePending
	return f
}
