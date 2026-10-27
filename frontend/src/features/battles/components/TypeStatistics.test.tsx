import { render, screen, within } from '@testing-library/react'
import { expect, it } from 'vitest'
import type { FactionStatistics, UnitTypeStatistics } from '../api/battles'
import { TypeStatistics } from './TypeStatistics'

const totals: FactionStatistics = {
  factionId: 'a', castleHealth: 0, unitsStarted: 2, unitsRecruited: 3, unitsLost: 4, unitsRemaining: 1, damageDealt: 90, damageTaken: 120,
  attackTicks: 10, defendTicks: 2, goldEarned: 300, goldSpent: 250, goldLooted: 15, buildingsConstructed: 1, buildingsDestroyed: 0,
}
const unit = (unitType: string, values: Partial<UnitTypeStatistics> = {}): UnitTypeStatistics => ({
  unitType, started: 0, recruited: 0, lost: 0, kills: 0, buildingsDestroyed: 0, damageDealt: 0, damageTaken: 0, healthRestored: 0, goldSpent: 0, ...values,
})

it('tabulates units and buildings by type and lists upgrades with their tick', () => {
  render(<TypeStatistics factionName="Amber" stats={{
    ...totals,
    unitTypes: [unit('heavy_cavalry', { started: 1, recruited: 2, lost: 1, kills: 4, damageDealt: 80, goldSpent: 240 })],
    buildingTypes: [{ buildingType: 'defense_tower', constructed: 1, lost: 0, kills: 2, damageDealt: 10, damageTaken: 5, goldSpent: 60 }],
    upgrades: [{ upgradeId: 'knighthood', tick: 120 }],
  }} />)
  const units = screen.getByRole('table', { name: 'Amber units by type' })
  expect(within(units).getByRole('rowheader')).toHaveTextContent('Heavy Cavalry')
  expect(within(units).getAllByRole('cell').map((cell) => cell.textContent)).toEqual(['3', '1', '4', '80', '240'])
  expect(within(units).queryByRole('columnheader', { name: 'Healed' })).not.toBeInTheDocument()
  const buildings = screen.getByRole('table', { name: 'Amber buildings by type' })
  expect(within(buildings).getAllByRole('cell').map((cell) => cell.textContent)).toEqual(['1', '0', '2', '60'])
  expect(screen.getByRole('list', { name: 'Amber upgrade timeline' })).toHaveTextContent('TICK 120')
})

it('adds a healed column only when a support unit restored health', () => {
  render(<TypeStatistics factionName="Amber" stats={{ ...totals, unitTypes: [unit('herbalist', { recruited: 1, healthRestored: 36 }), unit('vanguard', { started: 2 })] }} />)
  const table = screen.getByRole('table', { name: 'Amber units by type' })
  expect(within(table).getByRole('columnheader', { name: 'Healed' })).toBeInTheDocument()
  expect(within(table).getAllByRole('row')[1]).toHaveTextContent('36')
  expect(screen.queryByRole('table', { name: 'Amber buildings by type' })).not.toBeInTheDocument()
  expect(screen.queryByRole('list')).not.toBeInTheDocument()
})

it('renders nothing for results written before per-type statistics', () => {
  const { container } = render(<TypeStatistics factionName="Amber" stats={totals} />)
  expect(container).toBeEmptyDOMElement()
  render(<TypeStatistics factionName="Amber" stats={{ ...totals, unitTypes: [], buildingTypes: [], upgrades: [] }} />)
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
})
