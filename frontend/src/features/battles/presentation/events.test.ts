import { expect, it } from 'vitest'
import { eventSummary, matchesPreset } from './events'
import type { BattleEvent } from '../api/battles'

const event = (type: string, data: BattleEvent['data'] = {}): BattleEvent => ({ type, data, attempt: 1, sequence: 1, tick: 1, createdAt: '' })
it('describes repair amounts and costs without stringifying malformed payloads', () => {
  const naming = { factionNames: new Map<string, string>() }
  expect(eventSummary(event('unit_repaired', { amount: 4, cost: 2 }), naming)).toBe('An engineer repaired an ally for 4 HP (2 gold).')
  expect(eventSummary(event('unit_repaired', { amount: {}, cost: [] }), naming)).toBe('An engineer repaired an ally for an unknown amount (unknown cost).')
  expect(matchesPreset(event('unit_repaired'), 'combat')).toBe(true)
})

it('keeps significant events while excluding routine traffic and retaining unknown types in All', () => {
  for (const type of ['unit_queued', 'castle_destroyed', 'unit_destroyed', 'building_constructed', 'unit_recruited', 'upgrade_purchased', 'battle_failed']) expect(matchesPreset(event(type), 'key')).toBe(true)
  for (const type of ['unit_attacked', 'unit_moved', 'income_collected', 'tick_completed', 'future_event']) expect(matchesPreset(event(type), 'key')).toBe(false)
  expect(matchesPreset(event('future_event'), 'all')).toBe(true)
  expect(matchesPreset(event('unit_attacked'), 'combat')).toBe(true)
  expect(matchesPreset(event('income_collected'), 'economy')).toBe(true)
})
it('summarizes structured references without guessing unknown payloads', () => {
  const naming = { factionNames: new Map([['a', 'Amber']]) }
  expect(eventSummary(event('unit_attacked', { attackerId: 'a:ranger:1', targetId: 'b:castle', damage: 3 }), naming)).toContain('Amber · Ranger #1 attacked')
  expect(eventSummary(event('unit_attacked', { outcome: 'miss' }), naming)).toContain('missed')
  expect(eventSummary(event('unit_attack_missed', { attackerId: 'a:ranger:1' }), naming)).toContain('missed its target')
  expect(eventSummary(event('unit_healed', { healerId: 'a:ranger:1', targetId: 'ally' }), naming)).toContain('treated')
  expect(eventSummary(event('building_attacked', { attackerId: 'tower', targetId: 'target' }), naming)).toContain('attacked')
  expect(eventSummary(event('opening_completed'), naming)).toBe('The opening phase ended.')
  expect(eventSummary(event('battle_completed', { winnerFactionId: 'a' }), naming)).toBe('Amber won the battle.')
  expect(eventSummary(event('battle_completed'), naming)).toBe('The battle ended.')
  expect(eventSummary(event('upgrade_purchased', { factionId: 'a', upgradeId: 'steel_armor' }), naming)).toContain('Amber purchased')
  expect(eventSummary(event('battle_paused'), naming)).toBe('The battle paused.')
  expect(eventSummary(event('unknown_event'), naming)).toBe('Unknown event.')
  expect(eventSummary(event('income_collected', { factionId: 'a', amount: 5 }), naming)).toBe('Amber collected 5 gold in income.')
  for (const type of ['unit_moved', 'unit_recruited', 'building_constructed', 'castle_destroyed', 'unit_destroyed', 'unit_died', 'building_destroyed', 'loot_collected', 'unknown_event']) expect(eventSummary(event(type), naming)).toMatch(/\.$/)
})

it('names the killer of destroyed units, buildings and castles when the event records one', () => {
  const naming = { factionNames: new Map([['a', 'Amber'], ['b', 'Basalt']]) }
  const killer = { killerId: 'a:heavy_cavalry:007', killerType: 'heavy_cavalry', killerFactionId: 'a' }
  expect(eventSummary(event('unit_destroyed', { unitId: 'b:ranger:002', unitType: 'ranger', ...killer }), naming)).toBe('Basalt · Ranger #2 was destroyed by Amber · Heavy Cavalry #7.')
  expect(eventSummary(event('building_destroyed', { buildingId: 'b:barracks:001', ...killer }), naming)).toBe('Basalt · Barracks #1 was destroyed by Amber · Heavy Cavalry #7.')
  expect(eventSummary(event('castle_destroyed', { castleId: 'b:castle', killerId: 'a:defense_tower:003' }), naming)).toBe('Basalt · Castle was destroyed by Amber · Defense Tower #3.')
  expect(eventSummary(event('unit_destroyed', { unitId: 'b:ranger:002' }), naming)).toBe('Basalt · Ranger #2 was lost.')
})


it('distinguishes paid training orders from deployed recruits', () => {
  const queued = event('unit_queued', { factionId: 'a', unitType: 'ranger' })
  expect(eventSummary(queued, {})).toContain('queued Ranger for training')
  expect(matchesPreset(queued, 'economy')).toBe(true)
  expect(matchesPreset(queued, 'combat')).toBe(false)
})
