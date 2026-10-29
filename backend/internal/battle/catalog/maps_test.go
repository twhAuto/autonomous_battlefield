// Checks every authored map plays a full scripted battle to a decision.
package catalog

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/gamedata"
	service "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

// raceSweepTransitions bounds each map battle under the race detector. What it can
// check here is the sweep's concurrency, battles sharing one catalog, which starts
// with the opening; the coverage pass plays every battle to its end.
const raceSweepTransitions = 100

// skipSequentialBattleUnderRace skips a battle played in one goroutine when the race
// detector is on: it reports only conflicting access between goroutines, so the
// replay would cost minutes on a shared CI agent and check nothing. The coverage pass
// still plays it.
func skipSequentialBattleUnderRace(t *testing.T) {
	t.Helper()
	if raceEnabled {
		t.Skip("one-goroutine battle; the coverage pass plays it")
	}
}

// Chokepoints and forward deposits can let both sides hold a front for the whole
// battle, so scripted bots must break through on every map before the shared tick
// limit. Turn order goes by seat, so swapping the two faction IDs must replay the same
// battle: each seat holds the same state every tick and the same seat wins.
func TestScriptedBotsFinishEveryMap(t *testing.T) {
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range c.IDs() {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			traces, winners := playBothIDOrders(t, c, id)
			if t.Failed() {
				return
			}
			last := traces[0][len(traces[0])-1].tick
			if winners[0] < 0 && !raceEnabled {
				t.Fatalf("scripted bots stalemated at tick %d", last)
			}
			if tick, differ := firstDifference(traces[0], traces[1]); differ {
				t.Fatalf("swapping faction IDs changed the battle at tick %d", tick)
			}
			if winners[0] != winners[1] {
				t.Fatalf("swapping faction IDs changed the winning seat from %d to %d", winners[0], winners[1])
			}
			t.Logf("winning seat=%d ticks=%d", winners[0], last)
		})
	}
}

// playBothIDOrders plays map id with faction a on the left, then b, as parallel
// subtests. The group returns once both finish, so every map's battles run at once
// and the slowest single battle sets the test's duration.
func playBothIDOrders(t *testing.T, c *Catalog, id string) ([2][]seatTick, [2]int) {
	traces, winners := [2][]seatTick{}, [2]int{}
	t.Run("runs", func(t *testing.T) {
		for run, sides := range [][2]string{{"a", "b"}, {"b", "a"}} {
			t.Run(sides[0]+"-left", func(t *testing.T) {
				t.Parallel()
				traces[run], winners[run] = playScriptedBattle(t, c, id, sides)
			})
		}
	})
	return traces, winners
}

// seatTick is one transition's state by seat, without faction IDs, so runs with
// different faction IDs compare directly. The opening transition has tick 0.
type seatTick struct {
	tick  uint64
	seats string
}

// playScriptedBattle runs map id to the end, or for raceSweepTransitions under the race
// detector, with factions sides[0] on the left and sides[1] on the right. It returns
// every transition by seat and the winning seat, or -1 for a draw or an unfinished
// battle.
func playScriptedBattle(t *testing.T, c *Catalog, id string, sides [2]string) ([]seatTick, int) {
	t.Helper()
	s, err := c.Resolve(id, [2]service.FactionIdentity{{ID: sides[0], Name: sides[0]}, {ID: sides[1], Name: sides[1]}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := game.NewMatch("scripted-"+id, s, s.MaximumTicks)
	if err != nil {
		t.Fatal(err)
	}
	trace := []seatTick{}
	var last game.Transition
	for !m.Finished() && (!raceEnabled || len(trace) < raceSweepTransitions) {
		if last, err = m.Advance(); err != nil {
			t.Fatal(err)
		}
		trace = append(trace, seatTick{last.Tick, seatState(last.State, sides[0]) + " | " + seatState(last.State, sides[1])})
	}
	if last.Result == nil || last.Result.WinnerFactionID == nil {
		return trace, -1
	}
	return trace, slices.Index(sides[:], *last.Result.WinnerFactionID)
}

// firstDifference returns the first transition tick where two runs differ, and false
// when they match. A run that ends early differs at its partner's next tick.
func firstDifference(first, second []seatTick) (uint64, bool) {
	for index := range max(len(first), len(second)) {
		switch {
		case index >= len(first):
			return second[index].tick, true
		case index >= len(second) || first[index] != second[index]:
			return first[index].tick, true
		}
	}
	return 0, false
}

// seatState describes one faction's gold, units and buildings without IDs.
func seatState(state game.State, factionID string) string {
	parts := []string{}
	for _, faction := range state.Factions {
		if faction.FactionID == factionID {
			parts = append(parts, fmt.Sprintf("gold %d earned %d spent %d", faction.Gold, faction.GoldEarned, faction.GoldSpent))
		}
	}
	entities := []string{}
	for _, unit := range state.Units {
		if unit.FactionID == factionID {
			entities = append(entities, fmt.Sprintf("%s@%d,%d:%d", unit.Type, unit.Position.X, unit.Position.Y, unit.Health))
		}
	}
	for _, building := range state.Buildings {
		if building.FactionID == factionID {
			entities = append(entities, fmt.Sprintf("%s@%d,%d:%d", building.Type, building.Position.X, building.Position.Y, building.Health))
		}
	}
	slices.Sort(entities)
	return strings.Join(append(parts, entities...), " ")
}

// Every map gives each castle home mines inside its build range, more mines toward
// the middle, and centre mines equally far from both castles; mirrored mines are
// equally far from their own castles (odd-row offsets can shift the far castle by a
// hex), and maps differ in total gold.
func TestMapsSpreadFiniteGoldFromHomeToCentre(t *testing.T) {
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	totals := map[string]int64{}
	for _, id := range c.IDs() {
		s, err := c.Resolve(id, [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
		if err != nil {
			t.Fatal(err)
		}
		r := s.Ruleset
		if !r.Buildings.GoldMinesEnabled() || !r.Buildings.ContestedTerritory || r.Buildings.MaximumBuildingCount != 0 || r.Buildings.MaximumEconomyCount != 0 || r.MaximumUnitCount != 50 {
			t.Fatalf("%s: ruleset %s v%s must use gold mines, contested territory, no building limits and 50 units", id, r.ID, r.Version)
		}
		m, err := game.NewMatch("mines-"+id, s, 1)
		if err != nil {
			t.Fatal(err)
		}
		checkMineLayout(t, id, s.Map, m.State().GoldMines, r.Buildings.CastleBuildRange)
		for _, mine := range m.State().GoldMines {
			totals[id] += mine.Reserve
		}
	}
	checkGoldTotals(t, c, totals)
}

// checkGoldTotals requires summaries to report each map's reserve and the scarcest
// map to hold under half the richest map's gold.
func checkGoldTotals(t *testing.T, c *Catalog, totals map[string]int64) {
	t.Helper()
	for _, summary := range c.Summaries() {
		if summary.Map.GoldMineReserve <= 0 || totals[summary.ID]%summary.Map.GoldMineReserve != 0 {
			t.Fatalf("%s summary reports %d gold per mine", summary.ID, summary.Map.GoldMineReserve)
		}
	}
	scarcest, richest := int64(1<<62), int64(0)
	for _, total := range totals {
		scarcest, richest = min(scarcest, total), max(richest, total)
	}
	if scarcest*2 > richest {
		t.Fatalf("maps need scarce and rich gold, totals %v", totals)
	}
}

func checkMineLayout(t *testing.T, id string, gameMap game.Map, mines []game.GoldMine, castleRange int) {
	t.Helper()
	castles := gameMap.CastleSpawns
	at := map[game.Position]bool{}
	for _, mine := range mines {
		at[mine.Position] = true
	}
	home, beyond, centre := [2]int{}, [2]int{}, 0
	for _, mine := range mines {
		p := mine.Position
		if mineBesideBuildingSpot(p, castles, mines) {
			t.Fatalf("%s: mine %v touches a castle or another mine, which no building may stand beside", id, p)
		}
		near, far := game.HexDistance(p, castles[0]), game.HexDistance(p, castles[1])
		mirror := game.Position{X: gameMap.Width - 1 - p.X, Y: p.Y}
		if !at[mirror] || min(near, far) != min(game.HexDistance(mirror, castles[0]), game.HexDistance(mirror, castles[1])) {
			t.Fatalf("%s: mine %v and its mirror are not equally far from their own castles", id, p)
		}
		switch {
		case near == far:
			centre++
		case near < far && near >= 2 && near <= castleRange:
			home[0]++
		case near < far:
			beyond[0]++
		}
	}
	if home[0] < 2 || beyond[0]+centre <= home[0] || centre == 0 {
		t.Fatalf("%s: %d home, %d mid-field and %d centre mines per side", id, home[0], beyond[0], centre)
	}
}

// Scripted bots spend their home mines and claim mines beyond castle range.
func TestScriptedBotsExpandBeyondHomeMines(t *testing.T) {
	skipSequentialBattleUnderRace(t)
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Resolve("frontier-siege", [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := game.NewMatch("expansion", s, s.MaximumTicks)
	if err != nil {
		t.Fatal(err)
	}
	castles := map[string]game.Position{"a": s.Map.CastleSpawns[0], "b": s.Map.CastleSpawns[1]}
	depleted, forward := map[string]bool{}, map[string]bool{}
	for !m.Finished() {
		transition, err := m.Advance()
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range transition.Events {
			recordMineEvent(event, castles, s.Ruleset.Buildings.CastleBuildRange, depleted, forward)
		}
	}
	for _, faction := range []string{"a", "b"} {
		if !depleted[faction] || !forward[faction] {
			t.Fatalf("faction %s: depleted a mine %v, claimed a forward mine %v", faction, depleted[faction], forward[faction])
		}
	}
}

// recordMineEvent notes a faction emptying a mine or building economy beyond castle range.
func recordMineEvent(event game.Event, castles map[string]game.Position, castleRange int, depleted, forward map[string]bool) {
	faction, _ := event.Data["factionId"].(string)
	switch {
	case event.Type == "gold_mine_depleted":
		depleted[faction] = true
	case event.Type == "building_constructed" && event.Data["buildingType"] == game.BuildingEconomy:
		site, _ := event.Data["position"].(game.Position)
		forward[faction] = forward[faction] || game.HexDistance(site, castles[faction]) > castleRange
	}
}

func mineBesideBuildingSpot(p game.Position, castles [2]game.Position, mines []game.GoldMine) bool {
	for _, castle := range castles {
		if game.HexDistance(p, castle) < 2 {
			return true
		}
	}
	for _, other := range mines {
		if other.Position != p && game.HexDistance(p, other.Position) < 2 {
			return true
		}
	}
	return false
}
