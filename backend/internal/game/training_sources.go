package game

import (
	"fmt"
	"slices"
	"sort"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
)

// IsTrainingBuilding identifies the production kinds supported by authored content.
func IsTrainingBuilding(kind string) bool {
	switch kind {
	case BuildingBarracks, BuildingArcheryRange, BuildingStables, BuildingSiegeWorkshop, BuildingInfirmary:
		return true
	default:
		return false
	}
}

// Authored rosters opt new content into specialized production. Frozen older
// scenarios without rosters retain their original castle/Barracks recruitment.
func (m *Match) specializedTraining() bool {
	if len(m.scenario.Ruleset.Buildings.CastleTrains) > 0 {
		return true
	}
	for _, b := range m.scenario.Ruleset.Structures {
		if len(b.Trains) > 0 {
			return true
		}
	}
	return false
}

func (m *Match) trainingRoster(kind string) []string {
	if !m.specializedTraining() {
		if kind != BuildingCastle && kind != BuildingBarracks {
			return nil
		}
		ids := make([]string, 0, len(m.scenario.Ruleset.Units))
		for _, u := range m.scenario.Ruleset.Units {
			ids = append(ids, u.ID)
		}
		return ids
	}
	if kind == BuildingCastle {
		return append([]string(nil), m.scenario.Ruleset.Buildings.CastleTrains...)
	}
	for _, b := range m.scenario.Ruleset.Structures {
		if b.Kind == kind {
			return append([]string(nil), b.Trains...)
		}
	}
	return nil
}

func (m *Match) trainablePriorities(kind string, priorities []string) []string {
	roster := m.trainingRoster(kind)
	result := []string{}
	for _, id := range priorities {
		if slices.Contains(roster, id) {
			result = append(result, id)
		}
	}
	return result
}

// Automatic orders prefer a ready source closest to the enemy castle, with ID
// as a deterministic tie break. Explicit orders never silently change source.
func (m *Match) defaultTrainingSource(factionID, unitType string) *Building {
	if !m.specializedTraining() {
		return m.castle(factionID)
	}
	enemy := m.castle(m.opponentID(factionID))
	if enemy == nil {
		return nil
	}
	var best *Building
	for i := range m.state.Buildings {
		b := &m.state.Buildings[i]
		if b.FactionID != factionID || b.Health <= 0 || b.ReadyAtTick > m.state.Tick || len(b.TrainingQueue) >= MaximumTrainingQueue || !slices.Contains(m.trainingRoster(b.Type), unitType) {
			continue
		}
		if closerTrainingSource(b, best, enemy.Position) {
			best = b
		}
	}
	return best
}

func (m *Match) chooseTrainingSource(factionID string, definitions map[string]UnitType, priorities []string, reserve int64) (*Building, []string) {
	var first *Building
	var firstID string
	for _, id := range priorities {
		source := m.defaultTrainingSource(factionID, id)
		if source == nil {
			continue
		}
		if first == nil {
			first, firstID = source, id
		}
		unit, ok := definitions[id]
		if ok && m.factionState(factionID).Gold-unit.Cost >= reserve {
			return source, []string{id}
		}
	}
	return first, []string{firstID}
}

// Frozen specialized scenarios without castle training retain their free starting
// Barracks. Current castles provide the opening queue and need no extra building.
// Historical Barracks are active at tick zero and obey ordinary spacing.
// Both castle hexes are reserved first. Barracks sites are then chosen in faction
// slot order; each placement respects all previously placed buildings.
func (m *Match) addStartingBarracks() error {
	if !m.specializedTraining() || len(m.scenario.Ruleset.Buildings.CastleTrains) > 0 {
		return nil
	}
	var definition BuildingType
	for _, b := range m.scenario.Ruleset.Structures {
		if b.Kind == BuildingBarracks {
			definition = b
		}
	}
	if len(definition.Trains) == 0 {
		return fmt.Errorf("%w: specialized production requires Barracks", ErrInvalidScenario)
	}
	for _, setup := range m.scenario.Factions {
		castle := m.castle(setup.ID)
		sites := m.legalConstructionSites(setup.ID)
		sort.SliceStable(sites, func(i, j int) bool {
			return HexDistance(Position{X: sites[i].X, Y: sites[i].Y}, castle.Position) < HexDistance(Position{X: sites[j].X, Y: sites[j].Y}, castle.Position)
		})
		if len(sites) == 0 {
			return fmt.Errorf("%w: no starting Barracks site for %s", ErrInvalidScenario, setup.ID)
		}
		site := Position{X: sites[0].X, Y: sites[0].Y}
		m.state.Buildings = append(m.state.Buildings, Building{ID: setup.ID + ":barracks:starting", FactionID: setup.ID, Type: BuildingBarracks, Trains: append([]string(nil), definition.Trains...), Position: site, BuildRange: effectiveBuildRange(definition.BuildRange), Health: definition.Health, MaxHealth: definition.Health, Defense: definition.Defense, Vision: definition.Vision, Cost: definition.Cost})
	}
	return nil
}

func closerTrainingSource(candidate, current *Building, enemy Position) bool {
	if current == nil {
		return true
	}
	distance, previous := HexDistance(candidate.Position, enemy), HexDistance(current.Position, enemy)
	return distance < previous || distance == previous && candidate.ID < current.ID
}

// Preserve explicit source identity; only omitted sources are selected automatically.
func (m *Match) resolveTrainingSource(factionID, sourceID string, definitions map[string]UnitType, priorities []string, reserve int64) (*Building, []string) {
	if sourceID == "" {
		return m.chooseTrainingSource(factionID, definitions, priorities, reserve)
	}
	return m.recruitmentSource(factionID, sourceID), priorities
}

// Engine-authored plans must pass the same frozen validator as model plans.
// Filter only scripted baselines, leaving submitted and reused plans intact.
func (m *Match) filterScriptedRecruitment(factionID string, plan *controller.PlanProposal) {
	if !m.specializedTraining() || plan.RecruitmentOrders == nil {
		return
	}
	orders := []controller.RecruitmentOrder{}
	for _, order := range *plan.RecruitmentOrders {
		source := m.defaultTrainingSource(factionID, order.UnitType)
		if order.SourceBuildingID != "" {
			source = m.recruitmentSource(factionID, order.SourceBuildingID)
		}
		if source != nil && source.ReadyAtTick <= m.state.Tick && slices.Contains(m.trainingRoster(source.Type), order.UnitType) && !m.unitLocked(factionID, order.UnitType) {
			orders = append(orders, order)
		}
	}
	plan.RecruitmentOrders = &orders
}
