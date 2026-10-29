// Orders the factions, units and buildings that act within a tick. Rulesets with
// alternating turns go by seat, the first seat alternating every tick, so faction IDs
// never decide who acts first; older rulesets keep ID order so their battles replay
// unchanged.

package game

import (
	"slices"
	"sort"
)

// factionOrder lists faction IDs in the order they act this tick: seat order, reversed
// when turns alternate and the second seat leads this tick.
func (m *Match) factionOrder() []string {
	ids := make([]string, 0, len(m.scenario.Factions))
	for _, setup := range m.scenario.Factions {
		ids = append(ids, setup.ID)
	}
	if m.scenario.Ruleset.AlternatingTurns && m.secondSeatLeads() {
		slices.Reverse(ids)
	}
	return ids
}

// secondSeatLeads reports whether the second seat acts first this tick. The lead
// changes every tick, on even ticks by default. With alternating windows the count
// restarts at each decision window and shifts by the window number, so consecutive
// window ticks have different leaders even when every window tick is even.
func (m *Match) secondSeatLeads() bool {
	tick, interval := m.state.Tick, uint64(m.scenario.DecisionIntervalTicks)
	if !m.scenario.Ruleset.AlternatingWindows || interval == 0 {
		return tick%2 == 0
	}
	return (tick%interval+tick/interval)%2 == 0
}

func (m *Match) factionRanks() map[string]int {
	ranks := make(map[string]int, len(m.scenario.Factions))
	for rank, id := range m.factionOrder() {
		ranks[id] = rank
	}
	return ranks
}

// unitTurnOrder lists unit indices in the order they act, fastest first.
func (m *Match) unitTurnOrder() []int {
	if m.scenario.Ruleset.AlternatingTurns {
		return m.alternatingTurnOrder()
	}
	indices := make([]int, len(m.state.Units))
	for index := range indices {
		indices[index] = index
	}
	sort.Slice(indices, func(i, j int) bool {
		left, right := m.state.Units[indices[i]], m.state.Units[indices[j]]
		if left.Speed != right.Speed {
			return left.Speed > right.Speed
		}
		return left.ID < right.ID
	})
	return indices
}

// alternatingTurnOrder lists the units that can act. Within a speed, the two factions'
// units take turns in faction order, each faction's in ID order.
func (m *Match) alternatingTurnOrder() []int {
	indices := make([]int, 0, len(m.state.Units))
	for index, unit := range m.state.Units {
		if unit.Health > 0 && unit.ReadyAtTick <= m.state.Tick {
			indices = append(indices, index)
		}
	}
	ranks := m.factionRanks()
	sort.Slice(indices, func(i, j int) bool {
		left, right := m.state.Units[indices[i]], m.state.Units[indices[j]]
		if left.Speed != right.Speed {
			return left.Speed > right.Speed
		}
		if left.FactionID != right.FactionID {
			return ranks[left.FactionID] < ranks[right.FactionID]
		}
		return left.ID < right.ID
	})
	type tier struct {
		speed     int64
		factionID string
	}
	turn, taken := make(map[int]int, len(indices)), map[tier]int{}
	for _, index := range indices {
		unit := m.state.Units[index]
		key := tier{unit.Speed, unit.FactionID}
		turn[index] = taken[key]
		taken[key]++
	}
	sort.SliceStable(indices, func(i, j int) bool {
		left, right := m.state.Units[indices[i]], m.state.Units[indices[j]]
		if left.Speed != right.Speed {
			return left.Speed > right.Speed
		}
		return turn[indices[i]] < turn[indices[j]]
	})
	return indices
}

// sortBuildings orders building indices for towers, training and production: by
// faction order, then ID, with alternating turns; by ID otherwise.
func (m *Match) sortBuildings(indices []int) {
	if !m.scenario.Ruleset.AlternatingTurns {
		sort.Slice(indices, func(i, j int) bool { return m.state.Buildings[indices[i]].ID < m.state.Buildings[indices[j]].ID })
		return
	}
	ranks := m.factionRanks()
	sort.Slice(indices, func(i, j int) bool {
		left, right := m.state.Buildings[indices[i]], m.state.Buildings[indices[j]]
		if left.FactionID != right.FactionID {
			return ranks[left.FactionID] < ranks[right.FactionID]
		}
		return left.ID < right.ID
	})
}
