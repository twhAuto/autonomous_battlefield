// Ranks each group from its completed fixtures: points, then head-to-head points among
// the tied entrants, castle-health difference, damage difference and entry order.

package league

import "sort"

type Standing struct {
	Entrant          int
	Group            int
	Position         int
	Played           int
	Won              int
	Drawn            int
	Lost             int
	Points           int
	CastleDifference int64
	DamageDifference int64
}

// GroupStandings ranks one group. Void fixtures count for nobody.
func (l *League) GroupStandings(group int) []Standing {
	table := map[int]*Standing{}
	members := GroupMembers(l.Entrants, group)
	for _, member := range members {
		table[member] = &Standing{Entrant: member, Group: group}
	}
	for _, f := range l.groupFixtures(group) {
		score, ok := l.Score(f)
		if !ok {
			continue
		}
		record(table[f.Home], score.Winner, f.Home, score.CastleDifference, score.DamageDifference)
		record(table[f.Away], score.Winner, f.Away, -score.CastleDifference, -score.DamageDifference)
	}
	standings := make([]Standing, 0, len(members))
	for _, member := range members {
		standings = append(standings, *table[member])
	}
	l.rank(standings)
	return standings
}

// AllStandings ranks every group, indexed by entrant.
func (l *League) AllStandings() map[int]Standing {
	byEntrant := map[int]Standing{}
	for group := range l.GroupCount {
		for _, standing := range l.GroupStandings(group) {
			byEntrant[standing.Entrant] = standing
		}
	}
	return byEntrant
}

func record(s *Standing, winner *int, self int, castle, damage int64) {
	s.Played++
	s.CastleDifference += castle
	s.DamageDifference += damage
	switch {
	case winner == nil:
		s.Drawn++
		s.Points += PointsDraw
	case *winner == self:
		s.Won++
		s.Points += PointsWin
	default:
		s.Lost++
	}
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

// rank orders standings and assigns positions. Head-to-head points are counted within
// each set of entrants level on points.
func (l *League) rank(standings []Standing) {
	headToHead := map[int]int{}
	for _, tied := range levelSets(standings) {
		for entrant, points := range l.headToHeadPoints(tied) {
			headToHead[entrant] = points
		}
	}
	sort.Slice(standings, func(i, j int) bool {
		a, b := standings[i], standings[j]
		if a.Points != b.Points {
			return a.Points > b.Points
		}
		if headToHead[a.Entrant] != headToHead[b.Entrant] {
			return headToHead[a.Entrant] > headToHead[b.Entrant]
		}
		if a.CastleDifference != b.CastleDifference {
			return a.CastleDifference > b.CastleDifference
		}
		if a.DamageDifference != b.DamageDifference {
			return a.DamageDifference > b.DamageDifference
		}
		return a.Entrant < b.Entrant
	})
	for i := range standings {
		standings[i].Position = i + 1
	}
}

func levelSets(standings []Standing) [][]int {
	byPoints := map[int][]int{}
	for _, s := range standings {
		byPoints[s.Points] = append(byPoints[s.Points], s.Entrant)
	}
	sets := [][]int{}
	for _, entrants := range byPoints {
		if len(entrants) > 1 {
			sets = append(sets, entrants)
		}
	}
	return sets
}

func (l *League) headToHeadPoints(tied []int) map[int]int {
	inSet := map[int]bool{}
	for _, entrant := range tied {
		inSet[entrant] = true
	}
	points := map[int]int{}
	for _, f := range l.Fixtures {
		if f.Stage != StageGroup || !inSet[f.Home] || !inSet[f.Away] {
			continue
		}
		score, ok := l.Score(f)
		if !ok {
			continue
		}
		if score.Winner == nil {
			points[f.Home] += PointsDraw
			points[f.Away] += PointsDraw
		} else {
			points[*score.Winner] += PointsWin
		}
	}
	return points
}
