// Checks battle viewing: connection states and manual retry, tick progress, perspective
// selection, inspector gating by role, and pause/resume/stop control outcomes.

import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Link, MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { BattlePage } from './BattlePage'
import {
  BattleApiError, getBattle, getBattleView, listBattleEvents, listDecisionHistory,
  pauseBattle, resumeBattle, stopBattle, type BattleRun,
} from '../api/battles'
import { SessionContext } from '../../auth/SessionContext'

vi.mock('../api/battles', async (original) => ({
  ...await original<typeof import('../api/battles')>(),
  getBattle: vi.fn(), getBattleView: vi.fn(), listBattleEvents: vi.fn(), listDecisionHistory: vi.fn(),
  pauseBattle: vi.fn(), resumeBattle: vi.fn(), stopBattle: vi.fn(),
}))

const battleID = '33333333-3333-4333-8333-333333333333'
const north = '11111111-1111-4111-8111-111111111111'
const south = '22222222-2222-4222-8222-222222222222'

function runningBattle(overrides: Partial<BattleRun> = {}): BattleRun {
  const map = {
    kind: 'authored', width: 5, height: 3, rows: ['.....', '.....', '.....'],
    castleSpawns: [{ x: 1, y: 1 }, { x: 3, y: 1 }], economySites: [[], []],
  }
  return {
    id: battleID, ownerId: 'admin', scenarioId: 'castle-siege-v1', scenarioDigest: 'digest',
    requestedTicks: 200, currentTick: 50, status: 'running', paused: false, attempt: 1,
    participants: [{ id: north, name: 'North' }, { id: south, name: 'South' }],
    result: null,
    map,
    state: {
      battleId: battleID, tick: 50, units: [],
      factions: [
        { factionId: north, gold: 100, goldEarned: 100, goldSpent: 0, goldLooted: 0 },
        { factionId: south, gold: 80, goldEarned: 80, goldSpent: 0, goldLooted: 0 },
      ],
      buildings: [
        { id: `${north}:castle`, factionId: north, type: 'castle', position: { x: 1, y: 1 }, health: 300, maxHealth: 300, defense: 8, vision: 4, income: 10 },
        { id: `${south}:castle`, factionId: south, type: 'castle', position: { x: 3, y: 1 }, health: 300, maxHealth: 300, defense: 8, vision: 4, income: 10 },
      ],
    },
    ...overrides,
  } as BattleRun
}

// Serves `initial` from status polls until the control call resolves, then serves its
// result, as the server would. Keeps a poll that lands after the control from reverting
// the page to the pre-control state on slow runners.
function serveControlledBattle(control: typeof pauseBattle, initial: BattleRun, result: BattleRun) {
  let current = initial
  vi.mocked(getBattle).mockImplementation(async () => current)
  vi.mocked(control).mockImplementation(async () => { current = result; return result })
}

function renderBattle(role: 'admin' | 'player' = 'admin', actorID = 'admin') {
  return render(<MemoryRouter initialEntries={[`/battles/${battleID}`]}>
    <SessionContext.Provider value={{ enabled: true, authenticated: true, actor: { id: actorID, role } }}>
      <Link to="/battles/another">Another battle</Link>
      <Routes><Route path="/battles/:id" element={<BattlePage />} /></Routes>
    </SessionContext.Provider>
  </MemoryRouter>)
}

beforeEach(() => {
  vi.mocked(getBattle).mockReset().mockResolvedValue(runningBattle({ status: 'completed' }))
  vi.mocked(getBattleView).mockReset().mockRejectedValue(new BattleApiError('No perspective', 404))
  vi.mocked(listBattleEvents).mockReset().mockResolvedValue({ attempt: 1, events: [], nextCursor: null })
  vi.mocked(listDecisionHistory).mockReset().mockResolvedValue([])
  vi.mocked(pauseBattle).mockReset()
  vi.mocked(resumeBattle).mockReset()
  vi.mocked(stopBattle).mockReset()
})
afterEach(() => { vi.useRealTimers() })

it('shows the connecting state before any battle status arrives', async () => {
  vi.mocked(getBattle).mockImplementation(() => new Promise(() => {}))
  renderBattle()
  expect(screen.getByText('Locating battle')).toBeInTheDocument()
  expect(screen.getByText('ESTABLISHING LINK')).toBeInTheDocument()
})

it('renders tick progress and faction names once the battle settles', async () => {
  renderBattle()
  expect(await screen.findByRole('progressbar', { name: 'Tick limit used' })).toHaveValue(50)
  expect(screen.getByRole('progressbar', { name: 'Tick limit used' })).toHaveAttribute('max', '200')
  expect(screen.getByText(/of 200 maximum/)).toHaveTextContent('Tick 50 of 200 maximum')
  expect(screen.getAllByText('North').length).toBeGreaterThan(0)
})

it('shows when the battle data was last updated', async () => {
  renderBattle()
  const updated = await screen.findByText('UPDATED')
  expect(updated.querySelector('time')).toHaveAttribute('dateTime', expect.stringMatching(/^\d{4}-\d{2}-\d{2}T/))
})

it('offers a manual retry after the status link fails, then recovers', async () => {
  vi.useFakeTimers()
  vi.mocked(getBattle).mockRejectedValue(new BattleApiError('Engine unreachable', 503))
  renderBattle()
  // Five bounded reconnect attempts precede the terminal error state.
  await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
  expect(screen.getByText('Battle unavailable')).toBeInTheDocument()
  expect(screen.getByText('Engine unreachable')).toBeInTheDocument()
  vi.mocked(getBattle).mockResolvedValue(runningBattle({ status: 'completed' }))
  const retry = screen.getByRole('button', { name: 'RETRY CONNECTION' })
  await act(async () => { retry.click() })
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  expect(screen.getByRole('progressbar', { name: 'Tick limit used' })).toBeInTheDocument()
})

it('reports a reconnecting link without offering manual retry', async () => {
  vi.useFakeTimers()
  vi.mocked(getBattle).mockRejectedValue(new BattleApiError('Transient failure', 502))
  renderBattle()
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  expect(screen.getByText('Battle unavailable')).toBeInTheDocument()
  expect(screen.getByText(/Reconnecting 1\/5/)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'RETRY CONNECTION' })).not.toBeInTheDocument()
})

it('lets an owner pause a running battle and reflects the returned state', async () => {
  const user = userEvent.setup()
  serveControlledBattle(pauseBattle, runningBattle(), runningBattle({ paused: true }))
  renderBattle()
  await user.click(await screen.findByRole('button', { name: 'PAUSE' }))
  await waitFor(() => expect(pauseBattle).toHaveBeenCalledWith(battleID))
  expect(await screen.findByRole('button', { name: 'PLAY' })).toBeEnabled()
})

it('resumes a paused battle', async () => {
  const user = userEvent.setup()
  serveControlledBattle(resumeBattle, runningBattle({ paused: true }), runningBattle({ paused: false }))
  renderBattle()
  await user.click(await screen.findByRole('button', { name: 'PLAY' }))
  await waitFor(() => expect(resumeBattle).toHaveBeenCalledWith(battleID))
  expect(await screen.findByRole('button', { name: 'PAUSE' })).toBeEnabled()
})

it('stops a battle and settles on the terminal status', async () => {
  const user = userEvent.setup()
  serveControlledBattle(stopBattle, runningBattle(), runningBattle({ status: 'stopped' }))
  renderBattle()
  await user.click(await screen.findByRole('button', { name: 'STOP' }))
  await waitFor(() => expect(stopBattle).toHaveBeenCalledWith(battleID))
  await waitFor(() => expect(screen.queryByRole('button', { name: 'STOP' })).not.toBeInTheDocument())
})

it('surfaces a failed control request without losing the battle view', async () => {
  const user = userEvent.setup()
  vi.mocked(getBattle).mockResolvedValue(runningBattle())
  vi.mocked(pauseBattle).mockRejectedValue(new BattleApiError('Battle already completed', 409))
  renderBattle()
  await user.click(await screen.findByRole('button', { name: 'PAUSE' }))
  expect(await screen.findByText('Battle already completed')).toBeInTheDocument()
  expect(screen.getByRole('progressbar', { name: 'Tick limit used' })).toBeInTheDocument()
})

it('falls back to a generic message when a control fails unexpectedly', async () => {
  const user = userEvent.setup()
  vi.mocked(getBattle).mockResolvedValue(runningBattle())
  vi.mocked(pauseBattle).mockRejectedValue('offline')
  renderBattle()
  await user.click(await screen.findByRole('button', { name: 'PAUSE' }))
  expect(await screen.findByText('The battle control could not be applied.')).toBeInTheDocument()
})

it('hides owner controls from a non-owning player', async () => {
  vi.mocked(getBattle).mockResolvedValue(runningBattle())
  renderBattle('player', 'someone-else')
  await screen.findByRole('progressbar', { name: 'Tick limit used' })
  expect(screen.queryByRole('button', { name: 'PAUSE' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'STOP' })).not.toBeInTheDocument()
})

it('shows the inspector to an admin and withholds it from a player', async () => {
  vi.mocked(getBattle).mockResolvedValue(runningBattle({ status: 'completed' }))
  const user = userEvent.setup()
  const admin = renderBattle('admin')
  await screen.findByRole('progressbar', { name: 'Tick limit used' })
  expect(screen.queryByRole('combobox', { name: 'Perspective' })).not.toBeInTheDocument()
  const inspector = screen.getByRole('combobox', { name: 'Inspect' })
  await user.selectOptions(inspector, north)
  expect(screen.getAllByRole('tab').length).toBeGreaterThan(0)
  admin.unmount()

  renderBattle('player', 'someone-else')
  await screen.findByRole('progressbar', { name: 'Tick limit used' })
  expect(screen.queryByRole('combobox', { name: 'Inspect' })).not.toBeInTheDocument()
  expect(screen.queryAllByRole('tab')).toHaveLength(0)
})


it('drops the previous battle and its controls when navigating to another battle', async () => {
  const user = userEvent.setup()
  vi.mocked(getBattle).mockResolvedValueOnce(runningBattle()).mockImplementation(() => new Promise(() => {}))
  renderBattle()
  await screen.findByRole('button', { name: 'PAUSE' })
  await user.click(screen.getByRole('link', { name: 'Another battle' }))
  expect(screen.getByText('Locating battle')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'PAUSE' })).not.toBeInTheDocument()
  expect(screen.queryByRole('region', { name: 'Map navigation' })).not.toBeInTheDocument()
})

it('announces the winner for a completed battle', async () => {
  vi.mocked(getBattle).mockResolvedValue(runningBattle({
    status: 'completed', currentTick: 89,
    result: { winnerFactionId: north, reason: 'castle_destroyed', ticksRun: 89, factions: [] },
  }))
  renderBattle()
  await screen.findByRole('progressbar', { name: 'Tick limit used' })
  expect(screen.getAllByText(/North/).length).toBeGreaterThan(0)
  expect(screen.getAllByText('Battle result').length).toBeGreaterThan(0)
})

it('falls back to the raw winner identifier when it matches no participant', async () => {
  vi.mocked(getBattle).mockResolvedValue(runningBattle({
    status: 'completed',
    result: { winnerFactionId: 'retired-faction', reason: 'castle_destroyed', ticksRun: 89, factions: [] },
  }))
  renderBattle()
  await screen.findByRole('progressbar', { name: 'Tick limit used' })
  expect(screen.getAllByText(/retired-faction/).length).toBeGreaterThan(0)
})
