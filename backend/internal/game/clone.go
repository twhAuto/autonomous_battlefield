// Copies simulation state and nested collections so snapshots and callers cannot mutate
// the live match through shared references.

package game

import "github.com/twh09/autonomous_battlefield/backend/internal/game/controller"

func CloneState(value State) State {
	value.UnitDeaths = append([]UnitDeath(nil), value.UnitDeaths...)
	value.Factions = append([]FactionState(nil), value.Factions...)
	for i := range value.Factions {
		value.Factions[i].OwnedUpgradeIDs = append([]string(nil), value.Factions[i].OwnedUpgradeIDs...)
	}
	value.Units = append([]Unit(nil), value.Units...)
	for index := range value.Units {
		value.Units[index].HealTargetPosition = clonePosition(value.Units[index].HealTargetPosition)
		value.Units[index].MovementPath = append([]Position(nil), value.Units[index].MovementPath...)
		value.Units[index].MovedFrom = clonePosition(value.Units[index].MovedFrom)
		value.Units[index].AttackTargetPosition = clonePosition(value.Units[index].AttackTargetPosition)
	}
	value.Buildings = append([]Building(nil), value.Buildings...)
	for i := range value.Buildings {
		value.Buildings[i].Trains = append([]string(nil), value.Buildings[i].Trains...)
		if value.Buildings[i].Production != nil {
			production := *value.Buildings[i].Production
			value.Buildings[i].Production = &production
		}
		value.Buildings[i].TrainingQueue = append([]TrainingOrder(nil), value.Buildings[i].TrainingQueue...)
		value.Buildings[i].AttackTargetPosition = clonePosition(value.Buildings[i].AttackTargetPosition)
	}
	return value
}

func clonePosition(value *Position) *Position {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func CloneMap(value Map) Map {
	value.Rows = append([]string(nil), value.Rows...)
	for i := range value.EconomySites {
		value.EconomySites[i] = append([]Position(nil), value.EconomySites[i]...)
	}
	return value
}

func CloneResult(value Result) Result {
	value.Factions = append([]FactionStats(nil), value.Factions...)
	for i := range value.Factions {
		faction := &value.Factions[i]
		faction.UnitTypes = append([]UnitTypeStats(nil), faction.UnitTypes...)
		faction.BuildingTypes = append([]BuildingTypeStats(nil), faction.BuildingTypes...)
		faction.Upgrades = append([]UpgradePurchase(nil), faction.Upgrades...)
	}
	if value.WinnerFactionID != nil {
		winner := *value.WinnerFactionID
		value.WinnerFactionID = &winner
	}
	return value
}

func CloneScenario(value Scenario) Scenario {
	value.Ruleset.Buildings.CastleTrains = append([]string(nil), value.Ruleset.Buildings.CastleTrains...)
	value.Ruleset.Structures = CloneBuildingTypes(value.Ruleset.Structures)
	value.Ruleset.Knowledge = CloneKnowledge(value.Ruleset.Knowledge)
	if value.Ruleset.ContentSource != nil {
		copy := *value.Ruleset.ContentSource
		value.Ruleset.ContentSource = &copy
	}
	value.Map = CloneMap(value.Map)
	value.Ruleset.Units = append([]UnitType(nil), value.Ruleset.Units...)
	value.Ruleset.Upgrades = append([]Upgrade(nil), value.Ruleset.Upgrades...)
	value.Factions = append([]FactionSetup(nil), value.Factions...)
	for i := range value.Factions {
		value.Factions[i].Blueprint = CloneBlueprint(value.Factions[i].Blueprint)
		value.Factions[i].FallbackPlan = controller.ClonePlanProposal(value.Factions[i].FallbackPlan)
		value.Factions[i].ScriptedPlans = controller.ClonePlanProposals(value.Factions[i].ScriptedPlans)
	}
	return value
}

func CloneBuildingTypes(values []BuildingType) []BuildingType {
	result := append([]BuildingType(nil), values...)
	for i := range result {
		result[i].Trains = append([]string(nil), result[i].Trains...)
	}
	return result
}
