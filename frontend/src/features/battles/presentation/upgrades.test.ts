import { describe, expect, it } from 'vitest'
import { ownedUpgradeLabels, upgradeLabel } from './upgrades'
import { eventSummary } from './events'

describe('upgrade levels', () => {
  it('shows only the highest owned level, regardless of purchase ID order', () => {
    expect(ownedUpgradeLabels(['arsenal_3', 'arsenal', 'arsenal_2', 'industry', 'plating_2', 'plating'])).toBe('Arsenal III, Industry I, Plating II')
    expect(ownedUpgradeLabels(['arsenal', 'arsenal_2', 'arsenal_3'])).toBe('Arsenal III')
    expect(ownedUpgradeLabels()).toBe('None')
    expect(ownedUpgradeLabels(['legacy_custom', 'legacy_custom'])).toBe('legacy_custom')
    expect(upgradeLabel('arsenal_4')).toBe('arsenal_4')
  })
  it('explains the purchased level and incremental price in the event feed', () => {
    expect(eventSummary({ attempt: 1, sequence: 1, tick: 10, createdAt: '', type: 'upgrade_purchased', data: { factionId: 'a', upgradeId: 'arsenal_2', cost: 50 } }, { factionNames: new Map([['a', 'Amber']]) })).toBe('Amber purchased Arsenal II for 50 gold.')
  })
})

it('names specialization effects without merging separate unit roles', () => {
  expect(ownedUpgradeLabels(['arsenal', 'arsenal_2', 'vanguard_bulwark', 'herbalist_reach'])).toBe('Arsenal II, Vanguard: Bulwark (+3 defense), Herbalist: Reach (+1 treatment range)')
  expect(upgradeLabel('siege_mobility')).toBe('Siege: Mobility (+1 movement)')
})
