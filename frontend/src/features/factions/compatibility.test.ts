import { describe, expect, it } from 'vitest'
import { compatibleWithScenario, rulesForScenario } from './compatibility'
import type { Faction, FactionRuleset } from './api/factions'
import type { ScenarioSummary } from '../battles/api/battles'
const rules = { id: 'r', units: [{ id: 'ranger' }, { id: 'striker' }], upgrades: [{ id: 'plating' }, { id: 'arsenal' }] } as FactionRuleset
const scenario: ScenarioSummary = { id: 's', rulesetId: 'r', allowedUnits: ['ranger'], allowedUpgrades: [], rulesVersion: '1', tickDuration: '0s', decisionIntervalTicks: 10, maximumTicks: 500, mapWidth: 15, mapHeight: 11 }
const faction = { rulesetId: 'r', blueprint: { units: [{ type: 'ranger', count: 1 }], upgradeIds: [], recruitmentPriority: ['ranger'] } } as unknown as Faction
describe('scenario compatibility', () => {
 it('filters units and supports no upgrades without mutating catalog', () => {
  const selected = rulesForScenario(rules, scenario)
  expect(selected.units.map(u => u.id)).toEqual(['ranger'])
  expect(selected.upgrades).toEqual([])
  expect(rules.units).toHaveLength(2)
  expect(rulesForScenario(rules).units).toHaveLength(2)
 })
 it('checks blueprint units, upgrades and recruitment priorities', () => {
  expect(compatibleWithScenario(faction, scenario)).toBe(true)
  expect(compatibleWithScenario({ ...faction, rulesetId: 'different' }, scenario)).toBe(false)
  expect(compatibleWithScenario({ ...faction, blueprint: { ...faction.blueprint, units: [{ type: 'striker', count: 1 }] } }, scenario)).toBe(false)
  expect(compatibleWithScenario({ ...faction, blueprint: { ...faction.blueprint, upgradeIds: ['plating'] } }, scenario)).toBe(false)
  expect(compatibleWithScenario({ ...faction, blueprint: { ...faction.blueprint, recruitmentPriority: ['striker'] } }, scenario)).toBe(false)
  expect(compatibleWithScenario(faction)).toBe(true)
  expect(compatibleWithScenario(faction, { id: 'old', rulesetId: 'r' } as ScenarioSummary)).toBe(true)
 })
})
