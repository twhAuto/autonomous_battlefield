import type { ScenarioSummary } from '../battles/api/battles'
import type { Faction, FactionRuleset } from './api/factions'

export function compatibleWithScenario(faction: Faction, scenario?: ScenarioSummary) {
  if (!scenario) return true
  if (scenario.rulesetId && faction.rulesetId !== scenario.rulesetId) return false
  if (scenario.allowedUnits == null && scenario.allowedUpgrades == null) return true
  if (!faction.blueprint) return false
  return faction.blueprint.units.every(unit => scenario.allowedUnits == null || scenario.allowedUnits.includes(unit.type))
    && faction.blueprint.upgradeIds.every(id => scenario.allowedUpgrades == null || scenario.allowedUpgrades.includes(id))
    && faction.blueprint.recruitmentPriority.every(id => scenario.allowedUnits == null || scenario.allowedUnits.includes(id))
}

export function rulesForScenario(ruleset: FactionRuleset, scenario?: ScenarioSummary): FactionRuleset {
  return { ...ruleset,
    units: ruleset.units.filter(unit => scenario?.allowedUnits == null || scenario.allowedUnits.includes(unit.id)),
    upgrades: ruleset.upgrades.filter(upgrade => scenario?.allowedUpgrades == null || scenario.allowedUpgrades.includes(upgrade.id)),
  }
}
