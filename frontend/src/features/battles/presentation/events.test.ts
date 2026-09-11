import { expect, it } from 'vitest'
import { eventSummary, matchesPreset } from './events'
import type { BattleEvent } from '../api/battles'

const event = (type: string, data: BattleEvent['data'] = {}): BattleEvent => ({ type, data, attempt: 1, sequence: 1, tick: 1, createdAt: '' })
it('keeps significant events while excluding routine traffic and retaining unknown types in All', () => {
  for (const type of ['castle_destroyed', 'unit_destroyed', 'building_constructed', 'unit_recruited', 'upgrade_purchased', 'battle_failed']) expect(matchesPreset(event(type), 'key')).toBe(true)
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
