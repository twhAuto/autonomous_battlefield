// Tallies units, upgrades and maps. Only sides whose results carry per-type statistics
// count towards units and upgrades; older results have none.

package analytics

import (
	"sort"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

type UnitStats struct {
	UnitType           string   `json:"unitType"`
	Fielded            int      `json:"fielded"`
	Recruited          int      `json:"recruited"`
	Lost               int      `json:"lost"`
	Kills              int      `json:"kills"`
	BuildingsDestroyed int      `json:"buildingsDestroyed"`
	DamageDealt        int64    `json:"damageDealt"`
	DamageTaken        int64    `json:"damageTaken"`
	HealthRestored     int64    `json:"healthRestored"`
	GoldSpent          int64    `json:"goldSpent"`
	KillsPerLoss       *float64 `json:"killsPerLoss"`
	DamagePerGold      *float64 `json:"damagePerGold"`
	// ShareInWins and ShareInLosses are this type's share of all units recruited by
	// winning and by losing sides; a type built more by winners is a likely strength.
	ShareInWins   float64 `json:"shareInWins"`
	ShareInLosses float64 `json:"shareInLosses"`
}

// ModelUnit is how much of a controller's recruited army one unit type made up.
type ModelUnit struct {
	Controller Controller `json:"controller"`
	UnitType   string     `json:"unitType"`
	Recruited  int        `json:"recruited"`
	ArmyShare  float64    `json:"armyShare"`
}

type UpgradeStats struct {
	UpgradeID        string  `json:"upgradeId"`
	Purchases        int     `json:"purchases"`
	PurchaseRate     float64 `json:"purchaseRate"`
	MeanPurchaseTick float64 `json:"meanPurchaseTick"`
	// The win rates compare sides that bought the upgrade with sides that did not.
	WinRateWhenPurchased *float64 `json:"winRateWhenPurchased"`
	WinRateWhenNot       *float64 `json:"winRateWhenNot"`
}

type MapStats struct {
	ScenarioID string      `json:"scenarioId"`
	Battles    int         `json:"battles"`
	Draws      int         `json:"draws"`
	DrawRate   float64     `json:"drawRate"`
	MeanTicks  float64     `json:"meanTicks"`
	Sides      [2]SideWins `json:"sides"`
}

// SideWins is one spawn side's record on a map; side 0 is the first spawn.
type SideWins struct {
	Wins    int     `json:"wins"`
	WinRate float64 `json:"winRate"`
}

type upgradeTally struct {
	purchases, purchaseWins int
	ticks                   uint64
}

type unitTally struct {
	sides, sideWins   int
	types             map[string]*UnitStats
	recruitedIn       map[int]map[string]int // by result: 1 win, -1 loss
	recruitedInTotal  map[int]int
	byController      map[Controller]map[string]int
	byControllerTotal map[Controller]int
	purchases         map[string]*upgradeTally
}

func newUnitTally() *unitTally {
	return &unitTally{types: map[string]*UnitStats{}, recruitedIn: map[int]map[string]int{1: {}, -1: {}}, recruitedInTotal: map[int]int{},
		byController: map[Controller]map[string]int{}, byControllerTotal: map[Controller]int{}, purchases: map[string]*upgradeTally{}}
}

func (t *unitTally) add(b Battle) {
	for side, s := range b.Sides {
		if s.Stats != nil && len(s.Stats.UnitTypes) > 0 {
			t.addSide(s, b.result(side))
		}
	}
}

// addSide adds one side with per-type statistics; result is 1 win, 0 draw, -1 loss.
func (t *unitTally) addSide(s Side, result int) {
	controller := controllerOf(s.Controller)
	t.sides++
	if result == 1 {
		t.sideWins++
	}
	if t.byController[controller] == nil {
		t.byController[controller] = map[string]int{}
	}
	for _, u := range s.Stats.UnitTypes {
		t.addUnit(u)
		if result != 0 {
			t.recruitedIn[result][u.UnitType] += u.Recruited
			t.recruitedInTotal[result] += u.Recruited
		}
		t.byController[controller][u.UnitType] += u.Recruited
		t.byControllerTotal[controller] += u.Recruited
	}
	for _, purchase := range s.Stats.Upgrades {
		t.addUpgrade(purchase.UpgradeID, purchase.Tick, result == 1)
	}
}

// addUnit adds one side's record for a unit type.
func (t *unitTally) addUnit(record game.UnitTypeStats) {
	if t.types[record.UnitType] == nil {
		t.types[record.UnitType] = &UnitStats{UnitType: record.UnitType}
	}
	u := t.types[record.UnitType]
	u.Fielded += record.Started + record.Recruited
	u.Recruited += record.Recruited
	u.Lost += record.Lost
	u.Kills += record.Kills
	u.BuildingsDestroyed += record.BuildingsDestroyed
	u.DamageDealt += record.DamageDealt
	u.DamageTaken += record.DamageTaken
	u.HealthRestored += record.HealthRestored
	u.GoldSpent += record.GoldSpent
}

func (t *unitTally) addUpgrade(id string, tick uint64, won bool) {
	if t.purchases[id] == nil {
		t.purchases[id] = &upgradeTally{}
	}
	u := t.purchases[id]
	u.purchases++
	u.ticks += tick
	if won {
		u.purchaseWins++
	}
}

func (t *unitTally) units() []UnitStats {
	result := make([]UnitStats, 0, len(t.types))
	for kind, u := range t.types {
		if u.Lost > 0 {
			ratio := float64(u.Kills) / float64(u.Lost)
			u.KillsPerLoss = &ratio
		}
		if u.GoldSpent > 0 {
			ratio := float64(u.DamageDealt) / float64(u.GoldSpent)
			u.DamagePerGold = &ratio
		}
		u.ShareInWins = share(t.recruitedIn[1][kind], t.recruitedInTotal[1])
		u.ShareInLosses = share(t.recruitedIn[-1][kind], t.recruitedInTotal[-1])
		result = append(result, *u)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UnitType < result[j].UnitType })
	return result
}

func (t *unitTally) byModel() []ModelUnit {
	result := []ModelUnit{}
	for controller, types := range t.byController {
		for kind, recruited := range types {
			result = append(result, ModelUnit{Controller: controller, UnitType: kind, Recruited: recruited, ArmyShare: share(recruited, t.byControllerTotal[controller])})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Controller != result[j].Controller {
			return result[i].Controller.less(result[j].Controller)
		}
		return result[i].UnitType < result[j].UnitType
	})
	return result
}

func (t *unitTally) upgrades() []UpgradeStats {
	result := make([]UpgradeStats, 0, len(t.purchases))
	for id, u := range t.purchases {
		stats := UpgradeStats{UpgradeID: id, Purchases: u.purchases, PurchaseRate: share(u.purchases, t.sides), MeanPurchaseTick: float64(u.ticks) / float64(u.purchases)}
		purchased := float64(u.purchaseWins) / float64(u.purchases)
		stats.WinRateWhenPurchased = &purchased
		if others := t.sides - u.purchases; others > 0 {
			rate := float64(t.sideWins-u.purchaseWins) / float64(others)
			stats.WinRateWhenNot = &rate
		}
		result = append(result, stats)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpgradeID < result[j].UpgradeID })
	return result
}

func maps(battles []Battle) []MapStats {
	byID := map[string]*MapStats{}
	ticks := map[string]uint64{}
	for _, b := range battles {
		if byID[b.ScenarioID] == nil {
			byID[b.ScenarioID] = &MapStats{ScenarioID: b.ScenarioID}
		}
		m := byID[b.ScenarioID]
		m.Battles++
		ticks[b.ScenarioID] += b.TicksRun
		for side := range b.Sides {
			if b.result(side) == 1 {
				m.Sides[side].Wins++
			}
		}
		if b.WinnerFactionID == nil {
			m.Draws++
		}
	}
	result := make([]MapStats, 0, len(byID))
	for id, m := range byID {
		m.DrawRate, m.MeanTicks = share(m.Draws, m.Battles), float64(ticks[id])/float64(m.Battles)
		for side := range m.Sides {
			m.Sides[side].WinRate = share(m.Sides[side].Wins, m.Battles)
		}
		result = append(result, *m)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ScenarioID < result[j].ScenarioID })
	return result
}

func share(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total)
}
