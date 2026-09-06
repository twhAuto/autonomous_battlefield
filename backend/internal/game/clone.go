package game

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func CloneState(value State) State {
	value.Factions = append([]FactionState(nil), value.Factions...)
	for i := range value.Factions {
		value.Factions[i].OwnedUpgradeIDs = append([]string(nil), value.Factions[i].OwnedUpgradeIDs...)
	}
	value.Units = append([]Unit(nil), value.Units...)
	for index := range value.Units {
		value.Units[index].MovedFrom = clonePosition(value.Units[index].MovedFrom)
		value.Units[index].AttackTargetPosition = clonePosition(value.Units[index].AttackTargetPosition)
	}
	value.Buildings = append([]Building(nil), value.Buildings...)
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
	if value.WinnerFactionID != nil {
		winner := *value.WinnerFactionID
		value.WinnerFactionID = &winner
	}
	return value
}

func CloneScenario(value Scenario) Scenario {
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
