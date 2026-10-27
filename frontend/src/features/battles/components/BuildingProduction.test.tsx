import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'
import { ProductionChips, TrainingQueue, productionDetail } from './BuildingProduction'
import type { BattleBuilding, BattleState } from '../api/battles'

const building: BattleBuilding = { id: 'a:castle', factionId: 'a', type: 'castle', position: { x: 1, y: 3 }, health: 300, maxHealth: 300, defense: 8, vision: 0, income: 10, trains: ['vanguard'], production: { unitType: 'vanguard', enabled: true } }
const paid = { unitType: 'vanguard', remainingTicks: 2, trainingTicks: 4, queuedAtTick: 0 }
const state: BattleState = { battleId: 'battle', tick: 2, units: [], factions: [], buildings: [building] }
const props = { state, factionId: 'a', factionName: 'North' }

it('shows a chip for every living training building of the faction, with no manual controls', () => {
  render(<ProductionChips {...props} state={{ ...state, buildings: [building, { ...building, id: 'b:castle', factionId: 'b' }, { ...building, id: 'a:dead', health: 0 }, { ...building, id: 'a:economy', type: 'economy', production: undefined, trains: [] }] }} />)
  const chips = within(screen.getByRole('list', { name: 'North production' })).getAllByRole('listitem')
  expect(chips).toHaveLength(1)
  expect(chips[0]).toHaveAttribute('title', 'Castle (1, 3) · On · Vanguard · Waiting to start')
  expect(chips[0]).toHaveClass('production-chip--idle')
  expect(screen.queryByRole('button')).not.toBeInTheDocument()
})

// Keyboard activation of <summary> is browser behavior jsdom lacks; Playwright covers it.
it('discloses full status from each chip and shares its exclusive group', async () => {
  const user = userEvent.setup()
  render(<ProductionChips {...props} group="g" state={{ ...state, buildings: [{ ...building, trainingQueue: [paid] }, { ...building, id: 'a:castle:2', production: { enabled: false } }] }} />)
  const [training, stopped] = screen.getAllByRole('listitem').map((item) => item.querySelector('details')!)
  expect(training).not.toHaveAttribute('open')
  await user.click(training.querySelector('summary')!)
  expect(training).toHaveAttribute('open')
  expect(training.querySelector('.production-chip__detail')).toHaveTextContent('Vanguard · 2/4 ticks remaining')
  await user.click(stopped.querySelector('summary')!)
  expect(stopped).toHaveAttribute('open')
  expect(stopped).toHaveAttribute('name', 'g')
})

it('renders nothing for a faction without training buildings', () => {
  const { container } = render(<ProductionChips {...props} factionId="b" />)
  expect(container).toBeEmptyDOMElement()
  render(<ProductionChips {...props} state={{ ...state, buildings: null }} />)
  expect(screen.queryByRole('list')).not.toBeInTheDocument()
})

it('marks training progress and each waiting, blocked, stopped and inactive state', () => {
  const chip = (overrides: Partial<BattleBuilding>, tick = 2) => {
    const { unmount } = render(<ProductionChips {...props} state={{ ...state, tick, buildings: [{ ...building, ...overrides }] }} />)
    const item = screen.getByRole('listitem')
    const result = { tone: [...item.classList].find((name) => name.startsWith('production-chip--')), title: item.getAttribute('title'), progress: screen.queryByRole('progressbar')?.getAttribute('value') }
    unmount()
    return result
  }
  expect(chip({ trainingQueue: [paid] })).toEqual({ tone: 'production-chip--training', title: 'Castle (1, 3) · On · Vanguard · Vanguard · 2/4 ticks remaining', progress: '2' })
  expect(chip({ production: { enabled: true, unitType: 'vanguard', waitingForGold: true } }).title).toContain('Waiting for gold')
  expect(chip({ production: { enabled: true, unitType: 'vanguard', waitingForUnitLimit: true } })).toMatchObject({ tone: 'production-chip--waiting', title: expect.stringContaining('faction unit limit reached') })
  expect(chip({ production: { enabled: true, unitType: 'cannon', waitingForUpgrade: true, waitingForUnitLimit: true } })).toMatchObject({ tone: 'production-chip--waiting', title: expect.stringContaining('Cannon · Waiting · unit locked until its upgrade is owned') })
  expect(chip({ trainingQueue: [{ ...paid, remainingTicks: 0 }], trainingBlocked: true })).toMatchObject({ tone: 'production-chip--blocked', title: expect.stringContaining('Ready · exit blocked') })
  expect(chip({ production: { unitType: 'striker', enabled: false }, trainingQueue: [paid] }).title).toContain('Finishing paid unit, then stopping')
  expect(chip({ production: { unitType: 'striker', enabled: true }, trainingQueue: [paid] }).title).toContain('Next: Striker')
  expect(chip({ production: { enabled: false } })).toMatchObject({ tone: 'production-chip--off', title: 'Castle (1, 3) · Off · No unit selected · Production stopped' })
  expect(chip({ readyAtTick: 3 })).toMatchObject({ tone: 'production-chip--inactive', title: expect.stringContaining('Activates at tick 3') })
})

it('describes historical FIFO queues', () => {
  expect(productionDetail({ ...building, production: undefined, trainingQueue: [paid, paid] }, 2)).toBe('Training queue · 2/12 · Vanguard · 2/4 ticks remaining')
  expect(productionDetail({ ...building, production: undefined, trainingQueue: [] }, 2)).toBe('Training queue empty')
  render(<ProductionChips {...props} state={{ ...state, buildings: [{ ...building, production: undefined, trainingQueue: [paid] }] }} />)
  expect(screen.getByRole('listitem')).toHaveClass('production-chip--training')
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

it('keeps paid progress visible when an enabled source has no selected unit type', () => {
  render(<TrainingQueue building={{ ...building, production: { enabled: true }, trainingQueue: [paid] }} />)
  expect(screen.getByText(/No unit selected/)).toBeVisible()
  expect(screen.getByRole('progressbar')).toHaveAttribute('value', '2')
  expect(screen.queryByText(/Next:/)).not.toBeInTheDocument()
})
