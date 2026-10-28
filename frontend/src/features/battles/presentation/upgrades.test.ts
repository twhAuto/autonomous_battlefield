import { describe, expect, it } from 'vitest'
import { highestOwnedUpgrades, upgradeLabel, upgradePresentation } from './upgrades'
import { eventSummary } from './events'

const ownedLabels = (ids?: string[]) => highestOwnedUpgrades(ids).map(upgradeLabel)

describe('upgrade levels', () => {
  it('shows only the highest owned level, regardless of purchase ID order', () => {
    expect(ownedLabels(['arsenal_3', 'arsenal', 'arsenal_2', 'industry', 'plating_2', 'plating'])).toEqual(['Arsenal III', 'Industry I', 'Plating II'])
    expect(ownedLabels(['arsenal', 'arsenal_2', 'arsenal_3'])).toEqual(['Arsenal III'])
    expect(ownedLabels()).toEqual([])
    expect(ownedLabels(['legacy_custom', 'legacy_custom'])).toEqual(['legacy_custom'])
    expect(upgradeLabel('arsenal_4')).toBe('arsenal_4')
  })
  it('explains the purchased level and incremental price in the event feed', () => {
    expect(eventSummary({ attempt: 1, sequence: 1, tick: 10, createdAt: '', type: 'upgrade_purchased', data: { factionId: 'a', upgradeId: 'arsenal_2', cost: 50 } }, { factionNames: new Map([['a', 'Amber']]) })).toBe('Amber purchased Arsenal II for 50 gold.')
    expect(eventSummary({ attempt: 1, sequence: 2, tick: 11, createdAt: '', type: 'upgrade_purchased', data: { factionId: 'a', upgradeId: 'gunpowder', cost: 1000 } }, { factionNames: new Map([['a', 'Amber']]) })).toBe('Amber purchased Gunpowder (unlocks Cannon, Gunner) for 1000 gold.')
  })
})

it('names specialization effects without merging separate unit roles', () => {
  expect(ownedLabels(['arsenal', 'arsenal_2', 'vanguard_bulwark', 'herbalist_reach'])).toEqual(['Arsenal II', 'Vanguard: Bulwark (+3 defense)', 'Herbalist: Reach (+1 treatment range)'])
  expect(upgradeLabel('siege_mobility')).toBe('Siege: Mobility (+1 movement)')
})

it('names unit unlocks and describes each kind of upgrade for its icon', () => {
  expect(upgradeLabel('knighthood')).toBe('Knighthood (unlocks Heavy Cavalry)')
  expect(upgradePresentation('plating_3')).toEqual({ label: 'Plating III', art: 'plating', level: 'III', effect: '+6 defense for every unit' })
  expect(upgradePresentation('gunpowder')).toEqual({ label: 'Gunpowder (unlocks Cannon, Gunner)', art: 'gunpowder', effect: 'Siege Workshops can train Cannons and Archery Ranges can train Gunners' })
  expect(upgradePresentation('siege_breach')).toEqual({ label: 'Siege: Breach (+8 building damage)', art: 'breach', target: 'catapult', effect: 'Catapults, Battering Rams and Cannons deal 8 more damage to buildings' })
  expect(upgradePresentation('steel')).toEqual({ label: 'steel', art: 'steel' })
})

it('states the cumulative bonus of each general level', () => {
  expect(['arsenal', 'arsenal_2', 'arsenal_3'].map((id) => upgradePresentation(id).effect)).toEqual([
    '+2 attack for every attacking unit', '+4 attack for every attacking unit', '+6 attack for every attacking unit',
  ])
  expect(upgradePresentation('industry').effect).toBe('+2 gold per tick from each economy building')
})
