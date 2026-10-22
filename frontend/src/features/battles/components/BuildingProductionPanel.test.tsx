import { render, screen, within } from '@testing-library/react'
import { expect, it } from 'vitest'
import { BuildingProductionPanel, TrainingQueue } from './BuildingProductionPanel'
import type { BattleBuilding, BattleState } from '../api/battles'

const building: BattleBuilding = { id: 'a:castle', factionId: 'a', type: 'castle', position: { x: 1, y: 3 }, health: 300, maxHealth: 300, defense: 8, vision: 0, income: 10, trains: ['vanguard'], production: { unitType: 'vanguard', enabled: true } }
const paid = { unitType: 'vanguard', remainingTicks: 2, trainingTicks: 4, queuedAtTick: 0 }
const state: BattleState = { battleId: 'battle', tick: 2, units: [], factions: [], buildings: [building] }
const props = { state, factionNames: new Map([['a', 'North'], ['b', 'South']]), paused: false, settled: false }

it('shows every living training building grouped by faction, with no manual controls', () => {
  render(<BuildingProductionPanel {...props} state={{ ...state, buildings: [building, { ...building, id: 'b:castle', factionId: 'b' }, { ...building, id: 'a:dead', health: 0 }, { ...building, id: 'a:economy', type: 'economy', production: undefined, trains: [] }] }} />)
  expect(screen.getByRole('region', { name: 'North production' })).toBeVisible()
  expect(screen.getByRole('region', { name: 'South production' })).toBeVisible()
  expect(screen.getAllByRole('listitem')).toHaveLength(2)
  expect(screen.queryByRole('button')).not.toBeInTheDocument()
  expect(screen.getAllByText('Waiting to start')).toHaveLength(2)
})

it('updates progress, selected next unit, blocked exits and disabled paid work', () => {
  const { rerender } = render(<TrainingQueue building={{ ...building, trainingQueue: [paid] }} />)
  expect(screen.getByRole('progressbar')).toHaveAttribute('value', '2')
  rerender(<TrainingQueue building={{ ...building, production: { unitType: 'striker', enabled: true }, trainingQueue: [paid] }} />)
  expect(screen.getByText('Next: Striker')).toBeVisible()
  rerender(<TrainingQueue building={{ ...building, production: { unitType: 'striker', enabled: false }, trainingQueue: [{ ...paid, remainingTicks: 0 }], trainingBlocked: true }} />)
  expect(screen.getByText(/Ready · exit blocked/)).toBeVisible()
  expect(screen.getByText('Finishing paid unit, then stopping')).toBeVisible()
  expect(screen.queryByText('Next: Striker')).not.toBeInTheDocument()
  rerender(<TrainingQueue building={{ ...building, production: { unitType: 'striker', enabled: false } }} />)
  expect(screen.getByText('Production stopped')).toBeVisible()
  expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
})

it('distinguishes gold waits, unconfigured sources, activation, paused and ended battles', () => {
  const { rerender } = render(<BuildingProductionPanel {...props} state={{ ...state, buildings: [{ ...building, production: { enabled: true, unitType: 'vanguard', waitingForGold: true } }] }} />)
  expect(screen.getByText(/Waiting for gold/)).toBeVisible()
  rerender(<BuildingProductionPanel {...props} paused state={{ ...state, buildings: [{ ...building, production: { enabled: false }, readyAtTick: 3 }] }} />)
  expect(screen.getByText(/No unit selected/)).toBeVisible()
  expect(screen.getByText(/training is paused/)).toBeVisible()
  expect(screen.getByText('Activates at tick 3')).toBeVisible()
  rerender(<BuildingProductionPanel {...props} settled />)
  expect(screen.getByText(/Battle ended/)).toBeVisible()
  rerender(<BuildingProductionPanel {...props} state={{ ...state, buildings: null }} />)
  expect(screen.queryByRole('region')).not.toBeInTheDocument()
})

it('retains historical FIFO queue presentation and unnamed faction fallback', () => {
  render(<BuildingProductionPanel {...props} factionNames={new Map()} state={{ ...state, buildings: [{ ...building, production: undefined, trainingQueue: [paid, paid] }] }} />)
  const panel = screen.getByRole('region', { name: 'a production' })
  expect(within(panel).getByText('Training queue · 2/12')).toBeVisible()
})

it('keeps paid progress visible when an enabled source has no selected unit type', () => {
  render(<TrainingQueue building={{ ...building, production: { enabled: true }, trainingQueue: [paid] }} />)
  expect(screen.getByText(/No unit selected/)).toBeVisible()
  expect(screen.getByRole('progressbar')).toHaveAttribute('value', '2')
  expect(screen.queryByText(/Next:/)).not.toBeInTheDocument()
})
