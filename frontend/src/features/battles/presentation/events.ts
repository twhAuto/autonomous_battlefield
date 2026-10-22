import { upgradeLabel } from './upgrades'
import type { BattleEvent } from '../api/battles'
import { eventStringLabel, type EntityNaming } from './entities'

export type EventPreset = 'key' | 'combat' | 'economy' | 'all'
const keyEvents = new Set(['battle_created', 'battle_queued', 'battle_started', 'battle_paused', 'battle_resumed', 'battle_stopped', 'battle_failed', 'battle_completed', 'opening_completed', 'castle_destroyed', 'unit_destroyed', 'unit_died', 'building_destroyed', 'building_constructed', 'unit_recruited', 'unit_queued', 'upgrade_purchased', 'upgrade_applied'])
const combatEvents = new Set(['unit_attacked', 'unit_attack_missed', 'unit_moved', 'unit_healed', 'unit_repaired', 'tower_attacked', 'building_attacked', 'castle_destroyed', 'unit_destroyed', 'unit_died', 'building_destroyed'])
const economyEvents = new Set(['income_collected', 'loot_collected', 'building_constructed', 'unit_recruited', 'unit_queued', 'upgrade_purchased', 'upgrade_applied'])

export function matchesPreset(event: BattleEvent, preset: EventPreset) {
  if (preset === 'all') return true
  return ({ key: keyEvents, combat: combatEvents, economy: economyEvents })[preset].has(event.type)
}

/** Summaries use only structured event fields; original payloads remain available. */
export function eventSummary(event: BattleEvent, naming: EntityNaming) {
  const label = (key: string, fallback: string) => typeof event.data[key] === 'string' ? eventStringLabel(key, event.data[key] as string, naming) : fallback
  const actor = label('factionId', 'A faction')
  const upgradeCost = typeof event.data.cost === 'number' ? ` for ${event.data.cost} gold` : ''
  const repairAmount = typeof event.data.amount === 'number' ? `${event.data.amount} HP` : 'an unknown amount'
  const repairCost = typeof event.data.cost === 'number' ? `${event.data.cost} gold` : 'unknown cost'
  const amount = typeof event.data.amount === 'number' ? `${event.data.amount} gold` : 'gold'
  switch (event.type) {
    case 'battle_created': case 'battle_queued': case 'battle_started': case 'battle_paused': case 'battle_resumed': case 'battle_stopped': case 'battle_failed': return `The battle ${event.type.slice(7)}.`
    case 'opening_completed': return 'The opening phase ended.'
    case 'upgrade_purchased': case 'upgrade_applied': return `${actor} ${event.type === 'upgrade_purchased' ? 'purchased' : 'applied'} ${upgradeLabel(label('upgradeId', 'an upgrade'))}${upgradeCost}.`
    case 'unit_attack_missed': return `${label('attackerId', 'A unit')} missed its target.`
    case 'unit_repaired': return `${label('engineerId', 'An engineer')} repaired ${label('targetId', 'an ally')} for ${repairAmount} (${repairCost}).`
    case 'unit_healed': return `${label('healerId', 'A healer')} treated ${label('targetId', 'an ally')}.`
    case 'building_attacked': case 'tower_attacked': return `${label('attackerId', 'A building')} attacked ${label('targetId', 'its target')}.`
    case 'unit_attacked': {
      const damage = typeof event.data.damage === 'number' ? ` for ${event.data.damage} damage` : ''
      return `${label('attackerId', 'A unit')} ${event.data.outcome === 'miss' ? 'missed' : 'attacked'} ${label('targetId', 'its target')}${damage}.`
    }
    case 'unit_moved': return `${label('unitId', 'A unit')} moved.`
    case 'unit_queued': return `${actor} queued ${label('unitType', 'reinforcements')} for training.`
    case 'unit_recruited': return `${actor} recruited ${label('unitType', 'reinforcements')}.`
    case 'building_constructed': return `${actor} constructed ${label('buildingType', 'a building')}.`
    case 'castle_destroyed': return `${label('castleId', 'A castle')} was destroyed.`
    case 'unit_destroyed': case 'unit_died': return `${label('unitId', 'A unit')} was lost.`
    case 'building_destroyed': return `${label('buildingId', 'A building')} was destroyed.`
    case 'income_collected': case 'loot_collected': return `${actor} collected ${amount}${event.type === 'loot_collected' ? ' in loot' : ' in income'}.`
    case 'battle_completed': return typeof event.data.winnerFactionId === 'string' ? `${label('winnerFactionId', 'A faction')} won the battle.` : 'The battle ended.'
    default: return `${event.type.charAt(0).toUpperCase()}${event.type.slice(1).replaceAll('_', ' ')}.`
  }
}
