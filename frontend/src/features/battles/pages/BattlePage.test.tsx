// Checks battle viewing: connection states and manual retry, tick progress, map
// selection, inspector gating by role, pause/resume/stop control outcomes, results over
// the map, and replay.

import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Link, MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { BattlePage } from './BattlePage'
import {
  BattleApiError, getBattle, getBattleReplay, listBattleEvents, listDecisionHistory,
  pauseBattle, resumeBattle, stopBattle, type BattleReplay, type BattleRun, type ReplayEvent,
} from '../api/battles'
import { SessionContext } from '../../auth/SessionContext'

vi.mock('../api/battles', async (original) => ({
  ...await original<typeof import('../api/battles')>(),
  getBattle: vi.fn(), getBattleReplay: vi.fn(), listBattleEvents: vi.fn(), listDecisionHistory: vi.fn(),
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
  vi.mocked(getBattleReplay).mockReset()
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

it('keeps the header to the scenario and a live connection badge', async () => {
  vi.mocked(getBattle).mockResolvedValue(runningBattle())
  const view = renderBattle()
  const header = await screen.findByRole('region', { name: 'Battle status' })
  expect(header).toHaveTextContent('SCENARIO castle-siege-v1')
  expect(header).toHaveTextContent('LIVE')
  expect(header).not.toHaveTextContent(/ATTEMPT|UPDATED|STATUS/)
  view.unmount()

  vi.mocked(getBattle).mockResolvedValue(runningBattle({ status: 'completed' }))
  renderBattle()
  expect(await screen.findByRole('heading', { name: 'Battle complete' })).toBeInTheDocument()
  expect(screen.getByRole('region', { name: 'Battle status' })).not.toHaveTextContent('SETTLED')
})

it('offers a way back to the league once a league battle settles', async () => {
  const league = { id: 'league-1', name: 'Autumn cup', fixtureId: 'f', stage: 'group', group: 'B', round: 1, leg: 1 }
  vi.mocked(getBattle).mockResolvedValue(runningBattle({ league }))
  const view = renderBattle()
  await screen.findByRole('heading', { name: 'Battle in progress' })
  expect(screen.queryByRole('link', { name: '← BACK TO LEAGUE' })).not.toBeInTheDocument()
  view.unmount()

  vi.mocked(getBattle).mockResolvedValue(runningBattle({ status: 'completed', league }))
  const settled = renderBattle()
  expect(await screen.findByRole('link', { name: '← BACK TO LEAGUE' })).toHaveAttribute('href', '/leagues/league-1')
  settled.unmount()

  vi.mocked(getBattle).mockResolvedValue(runningBattle({ status: 'completed' }))
  renderBattle()
  await screen.findByRole('heading', { name: 'Battle complete' })
  expect(screen.queryByRole('link', { name: '← BACK TO LEAGUE' })).not.toBeInTheDocument()
})

it('shows a result over the map until the viewer asks for the map', async () => {
  const user = userEvent.setup()
  vi.mocked(getBattle).mockResolvedValue(runningBattle({
    status: 'completed', currentTick: 89,
    result: { winnerFactionId: north, reason: 'castle_destroyed', ticksRun: 89, factions: [
      { factionId: north, castleHealth: 120, unitsStarted: 0, unitsRecruited: 9, unitsLost: 3, unitsRemaining: 6, damageDealt: 400, damageTaken: 90, attackTicks: 40, defendTicks: 10, goldEarned: 900, goldSpent: 800, goldLooted: 0, buildingsConstructed: 4, buildingsDestroyed: 1 },
    ] },
  }))
  renderBattle()
  const results = await screen.findByRole('region', { name: 'After-action statistics' })
  expect(results.closest('.battle-map-column')).toHaveClass('battle-map-column--results')
  expect(results).toHaveTextContent('89 TICKS')
  expect(results).toHaveTextContent('Damage dealt400')
  expect(screen.queryByRole('button', { name: 'Show results' })).not.toBeInTheDocument()
  // The covered map takes no focus and is hidden from assistive technology.
  const mapPanel = () => document.querySelector('.battle-map-column > .map-panel')
  expect(mapPanel()).toHaveAttribute('inert')

  await user.click(screen.getByRole('button', { name: 'Show map' }))
  expect(screen.queryByRole('region', { name: 'After-action statistics' })).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Show results' })).toHaveFocus()
  expect(document.querySelector('.battle-map-column')).not.toHaveClass('battle-map-column--results')
  expect(mapPanel()).not.toHaveAttribute('inert')

  await user.click(screen.getByRole('button', { name: 'Show results' }))
  expect(screen.getByRole('button', { name: 'Show map' })).toHaveFocus()
})

it('returns to the results after a replay started from the map', async () => {
  const user = userEvent.setup()
  vi.mocked(getBattle).mockResolvedValue(runningBattle({
    status: 'completed', currentTick: 2,
    result: { winnerFactionId: north, reason: 'castle_destroyed', ticksRun: 2, factions: [] },
  }))
  vi.mocked(getBattleReplay).mockResolvedValue(replaySegment())
  renderBattle()
  await user.click(await screen.findByRole('button', { name: 'Show map' }))
  await user.click(screen.getByRole('button', { name: 'WATCH REPLAY' }))
  await screen.findByText(/HEX TACTICAL MAP · TICK 0/)
  expect(screen.queryByRole('region', { name: 'After-action statistics' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'EXIT REPLAY' }))
  expect(screen.getByRole('region', { name: 'After-action statistics' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Show map' })).not.toHaveFocus()
})

it('lists results below the page when the battle has no map to cover', async () => {
  vi.mocked(getBattle).mockResolvedValue(runningBattle({
    status: 'completed', map: undefined as unknown as BattleRun['map'],
    result: { winnerFactionId: north, reason: 'castle_destroyed', ticksRun: 89, factions: [] },
  }))
  renderBattle()
  const results = await screen.findByRole('region', { name: 'After-action statistics' })
  expect(results).not.toHaveClass('battle-statistics--overlay')
  expect(screen.queryByRole('button', { name: 'Show map' })).not.toBeInTheDocument()
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
  expect(document.querySelector('.battle-cockpit')).toHaveClass('battle-cockpit--no-inspector')
})

it('widens, hides and restores the inspector without losing keyboard focus', async () => {
  vi.mocked(getBattle).mockResolvedValue(runningBattle({ status: 'completed' }))
  const user = userEvent.setup()
  renderBattle()
  const cockpit = (await screen.findByRole('progressbar', { name: 'Tick limit used' })).closest('.battle-cockpit')
  expect(cockpit).toHaveClass('battle-cockpit--inspector-normal')
  const widen = screen.getByRole('button', { name: 'Widen inspector' })
  await user.click(widen)
  expect(cockpit).toHaveClass('battle-cockpit--inspector-wide')
  expect(widen).toHaveAttribute('aria-pressed', 'true')
  await user.click(widen)
  expect(cockpit).toHaveClass('battle-cockpit--inspector-normal')
  await user.click(screen.getByRole('button', { name: 'Hide inspector' }))
  expect(cockpit).toHaveClass('battle-cockpit--inspector-hidden')
  expect(screen.queryByRole('complementary', { name: 'Battle inspector' })).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Show inspector' })).toHaveFocus()
  await user.click(screen.getByRole('button', { name: 'Show inspector' }))
  expect(screen.getByRole('complementary', { name: 'Battle inspector' })).toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: 'Inspect' })).toHaveFocus()
  expect(screen.queryByRole('button', { name: 'Show inspector' })).not.toBeInTheDocument()
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
  expect(screen.getByRole('complementary', { name: 'Battle result' })).toHaveTextContent('North wins')
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

// One segment for a two-tick battle: North's gold rises each tick and tick 1 has a move.
function replaySegment(): BattleReplay {
  const battle = runningBattle()
  return {
    battleId: battleID, attempt: 1, finalTick: 2, fromTick: 0, toTick: 2, nextTick: null,
    frames: [0, 1, 2].map((tick) => {
      const events: ReplayEvent[] = tick === 1
        ? [{ sequence: 4, tick, type: 'income_collected', data: { factionId: north, amount: 1 } }, { sequence: 5, tick, type: 'tick_completed', data: {} }]
        : [{ sequence: tick + 1, tick, type: 'tick_completed', data: {} }]
      return {
        tick, events,
        state: { ...battle.state, tick, factions: [{ factionId: north, gold: 10 + tick, goldEarned: 0, goldSpent: 0, goldLooted: 0 }, { factionId: south, gold: 5, goldEarned: 0, goldSpent: 0, goldLooted: 0 }] },
      }
    }),
  }
}

it('replays a settled battle tick by tick and returns to the final state', async () => {
  vi.mocked(getBattle).mockResolvedValue(runningBattle({ status: 'completed', currentTick: 2 }))
  vi.mocked(getBattleReplay).mockResolvedValue(replaySegment())
  const user = userEvent.setup()
  renderBattle('player', 'viewer')
  await user.click(await screen.findByRole('button', { name: 'WATCH REPLAY' }))

  const controls = screen.getByRole('region', { name: 'Replay controls' })
  expect(await screen.findByText(/HEX TACTICAL MAP · TICK 0/)).toBeInTheDocument()
  expect(getBattleReplay).toHaveBeenCalledWith(battleID, 0, expect.any(AbortSignal))
  expect(screen.getByRole('article', { name: 'North' })).toHaveTextContent('Gold10')

  await user.click(screen.getByRole('button', { name: 'Next tick' }))
  expect(screen.getByText(/HEX TACTICAL MAP · TICK 1/)).toBeInTheDocument()
  expect(screen.getByRole('article', { name: 'North' })).toHaveTextContent('Gold11')
  expect(controls).toHaveTextContent('TICK 1 / 2')
  // The map keeps the space; the tick's events stay in the inspector's event history.
  expect(screen.queryByRole('region', { name: /Tick \d+ events/ })).not.toBeInTheDocument()

  await user.click(screen.getByRole('button', { name: 'Restart replay' }))
  expect(screen.getByText(/HEX TACTICAL MAP · TICK 0/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'EXIT REPLAY' }))
  expect(screen.queryByRole('region', { name: 'Replay controls' })).not.toBeInTheDocument()
  expect(screen.getByText(/HEX TACTICAL MAP · TICK 50/)).toBeInTheDocument()
})

it('plays a replay at the selected speed and seeks from the timeline', async () => {
  vi.mocked(getBattle).mockResolvedValue(runningBattle({ status: 'stopped', currentTick: 2 }))
  vi.mocked(getBattleReplay).mockResolvedValue(replaySegment())
  const user = userEvent.setup()
  renderBattle()
  await user.click(await screen.findByRole('button', { name: 'WATCH REPLAY' }))
  await screen.findByText(/HEX TACTICAL MAP · TICK 0/)
  await user.selectOptions(screen.getByRole('combobox', { name: 'Speed' }), '8')
  await user.click(screen.getByRole('button', { name: 'PLAY' }))
  expect(await screen.findByText(/HEX TACTICAL MAP · TICK 2/)).toBeInTheDocument()
  expect(await screen.findByRole('button', { name: 'PLAY' })).toBeInTheDocument()

  // jsdom does not implement range keyboard input, so set the value as the slider would.
  fireEvent.change(screen.getByRole('slider', { name: 'Replay tick' }), { target: { value: '1' } })
  expect(screen.getByText(/HEX TACTICAL MAP · TICK 1/)).toBeInTheDocument()
})

it('explains a missing replay and retries it', async () => {
  vi.mocked(getBattle).mockResolvedValue(runningBattle({ status: 'completed', currentTick: 2 }))
  vi.mocked(getBattleReplay)
    .mockRejectedValueOnce(new BattleApiError('No replay was recorded for this part of the battle.', 409))
    .mockResolvedValue(replaySegment())
  const user = userEvent.setup()
  renderBattle()
  await user.click(await screen.findByRole('button', { name: 'WATCH REPLAY' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('No replay was recorded for this part of the battle.')
  expect(screen.getByRole('button', { name: 'PLAY' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText(/HEX TACTICAL MAP · TICK 0/)).toBeInTheDocument()
})

it('offers no replay while a battle is still running', async () => {
  vi.mocked(getBattle).mockResolvedValue(runningBattle())
  renderBattle()
  await screen.findByRole('progressbar', { name: 'Tick limit used' })
  expect(screen.queryByRole('button', { name: 'WATCH REPLAY' })).not.toBeInTheDocument()
})
