// Tracks finite gold mines: under a gold-mine ruleset every map gold deposit holds a
// reserve, economy buildings stand only on mines with gold left, and each economy
// building draws its income from its mine until the reserve runs out.

package game

import "github.com/twh09/autonomous_battlefield/backend/internal/game/controller"

// MaximumGoldMineReserve bounds authored per-mine reserves.
const MaximumGoldMineReserve int64 = 1_000_000

// GoldMinesEnabled reports whether gold deposits are finite mines that economy
// buildings require.
func (r BuildingRules) GoldMinesEnabled() bool { return r.GoldMineReserve > 0 }

// EconomyAvailable reports whether the ruleset offers economy buildings: a positive
// economy limit, or gold mines, whose economy count is unlimited by default.
func EconomyAvailable(r BuildingRules) bool {
	return r.MaximumEconomyCount > 0 || r.GoldMinesEnabled()
}

// economyLimitReached applies MaximumEconomyCount, where zero means unlimited only
// under gold mines; older rulesets with zero offer no economy buildings.
func economyLimitReached(r BuildingRules, count int) bool {
	return (r.MaximumEconomyCount > 0 || !r.GoldMinesEnabled()) && count >= r.MaximumEconomyCount
}

// GoldMineReserveFor is the gold each mine on gameMap starts with: the map's
// override, else the ruleset default; zero when the ruleset has no gold mines.
func GoldMineReserveFor(gameMap Map, rules BuildingRules) int64 {
	if !rules.GoldMinesEnabled() {
		return 0
	}
	if gameMap.GoldMineReserve > 0 {
		return gameMap.GoldMineReserve
	}
	return rules.GoldMineReserve
}

// initialGoldMines creates one full mine per gold deposit, in row order.
func initialGoldMines(gameMap Map, rules BuildingRules) []GoldMine {
	reserve := GoldMineReserveFor(gameMap, rules)
	if reserve == 0 {
		return nil
	}
	mines := []GoldMine{}
	for _, position := range allPositions(gameMap) {
		if gameMap.TerrainAt(position) == TerrainGold {
			mines = append(mines, GoldMine{Position: position, Reserve: reserve, Remaining: reserve})
		}
	}
	return mines
}

func (m *Match) goldMineAt(position Position) *GoldMine {
	for i := range m.state.GoldMines {
		if m.state.GoldMines[i].Position == position {
			return &m.state.GoldMines[i]
		}
	}
	return nil
}

// GoldMineTerrainReason is the static part of gold-mine placement: economy needs a
// gold deposit and other structures may not take one. Plan validation and execution
// share it; kind "" checks a site for any non-economy structure.
func GoldMineTerrainReason(gameMap Map, rules BuildingRules, kind string, site Position) string {
	if !rules.GoldMinesEnabled() {
		return ""
	}
	onMine := gameMap.TerrainAt(site) == TerrainGold
	if kind == BuildingEconomy && !onMine {
		return "requires_gold_mine"
	}
	if kind != BuildingEconomy && onMine {
		return "gold_mine_reserved"
	}
	return ""
}

// goldMineReason adds the dynamic checks: an economy site's mine still has gold, and
// no building may stand beside a mine with gold left, so no one can wall a mine off.
func (m *Match) goldMineReason(kind string, site Position) string {
	rules := m.scenario.Ruleset.Buildings
	if reason := GoldMineTerrainReason(m.scenario.Map, rules, kind, site); reason != "" || !rules.GoldMinesEnabled() {
		return reason
	}
	if mine := m.goldMineAt(site); kind == BuildingEconomy && (mine == nil || mine.Remaining <= 0) {
		return "gold_mine_depleted"
	}
	if m.besideLiveMine(site) {
		return "beside_gold_mine"
	}
	return ""
}

func (m *Match) besideLiveMine(site Position) bool {
	for _, mine := range m.state.GoldMines {
		if mine.Remaining > 0 && HexDistance(site, mine.Position) == 1 {
			return true
		}
	}
	return false
}

// economyIncome is one economy building's income this tick. Under gold mines it is
// drawn from the building's mine and capped at the gold left; extract false projects
// the amount without depleting the mine. A mine emptied by this draw returns true.
func (m *Match) economyIncome(building *Building, extract bool) (int64, bool) {
	if !m.scenario.Ruleset.Buildings.GoldMinesEnabled() {
		return building.Income, false
	}
	mine := m.goldMineAt(building.Position)
	if mine == nil {
		return 0, false
	}
	amount := min(building.Income, mine.Remaining)
	if !extract || amount <= 0 {
		return max(amount, 0), false
	}
	mine.Remaining -= amount
	return amount, mine.Remaining == 0
}

// legalGoldMineSites lists the mines a faction's economy building may take now.
func (m *Match) legalGoldMineSites(factionID string) []controller.Coordinate {
	if !m.scenario.Ruleset.Buildings.GoldMinesEnabled() {
		return nil
	}
	return m.legalSitesFor(factionID, BuildingEconomy)
}

// unclaimedGoldMines lists mines with gold left and no living building on or beside
// them, so building spacing still allows an economy building there.
func (m *Match) unclaimedGoldMines() []Position {
	mines := []Position{}
	for _, mine := range m.state.GoldMines {
		if mine.Remaining > 0 && !m.buildingWithin(mine.Position, 1) {
			mines = append(mines, mine.Position)
		}
	}
	return mines
}

func (m *Match) buildingWithin(site Position, distance int) bool {
	for _, b := range m.state.Buildings {
		if b.Health > 0 && HexDistance(b.Position, site) <= distance {
			return true
		}
	}
	return false
}
