import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { League } from '../api/leagues'
import { LeaguePage, leagueRefreshMs } from './LeaguePage'

const battleID = '11111111-1111-4111-8111-111111111111'

function sampleLeague(change: Partial<League> = {}): League {
  const names = ['Amber', 'Basalt', 'Cinder', 'Dusk', 'Ember', 'Frost', 'Gale', 'Haze']
  return {
    id: 'league-1', ownerId: 'admin', name: 'Autumn cup', status: 'running', groupCount: 2,
    settings: { scenarioIds: ['frontier-siege'], tickCount: 500 }, champion: null, progress: { settled: 1, total: 30 },
    entrants: names.map((factionName, index) => ({ index, group: index % 2 ? 'B' : 'A', factionId: `f${index}`, factionName,
      controller: index === 0 ? { type: 'model', modelIdentifier: 'sonnet', reasoningEffort: 'low' } : { type: 'scripted' } })),
    groups: (['A', 'B'] as const).map((group, offset) => ({ group, standings: [0, 2, 4, 6].map((base, position) => ({
      entrant: base + offset, position: position + 1, played: 1, won: position === 0 ? 1 : 0, drawn: 0, lost: position === 0 ? 0 : 1,
      points: position === 0 ? 3 : 0, castleDifference: position === 0 ? 120 : -40, damageDifference: 0,
    })) })),
    knockout: [{ stage: 'semifinal', tie: 1, homeEntrant: 0, awayEntrant: 3, winner: 0 }],
    fixtures: [
      { id: 'x1', sequence: 0, stage: 'group', group: 'A', round: 1, leg: 1, homeEntrant: 0, awayEntrant: 6, scenarioId: 'frontier-siege', status: 'completed', attempts: 2, battleId: battleID, result: { winner: 0, reason: 'castle_destroyed' } },
      { id: 'x2', sequence: 1, stage: 'group', group: 'B', round: 1, leg: 1, homeEntrant: 1, awayEntrant: 7, scenarioId: 'frontier-siege', status: 'pending', attempts: 0, battleId: null },
    ],
    createdAt: '2026-10-28T00:00:00Z', completedAt: null, ...change,
  }
}

const reply = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

function show(canCancel = true) {
  return render(<MemoryRouter initialEntries={['/leagues/league-1']}><Routes><Route path="/leagues/:id" element={<LeaguePage canCancel={canCancel} />} /></Routes></MemoryRouter>)
}

beforeEach(() => { vi.stubGlobal('fetch', vi.fn()) })
afterEach(() => { vi.useRealTimers() })

it('shows group tables, the bracket and fixtures', async () => {
  vi.mocked(fetch).mockImplementation(async () => reply(200, sampleLeague()))
  show(false)
  expect(await screen.findByRole('heading', { name: 'Autumn cup' })).toBeInTheDocument()
  const groupA = screen.getByRole('table', { name: 'Group A standings' })
  const rows = within(groupA).getAllByRole('row')
  expect(rows[1]).toHaveTextContent('Amber')
  expect(rows[1]).toHaveTextContent('sonnet · low')
  expect(rows[1]).toHaveTextContent('+120')
  expect(rows[1]).toHaveClass('league-table__qualifier')
  expect(rows[3]).not.toHaveClass('league-table__qualifier')
  const bracket = screen.getByRole('region', { name: 'Knockout bracket' })
  expect(within(bracket).getByRole('list', { name: 'Semifinals tie 1' })).toHaveTextContent(/^Amber.*\(winner\)Dusk/)
  expect(within(bracket).getAllByText('To be decided')).toHaveLength(2)
  const fixtures = screen.getByRole('table', { name: 'Fixtures in play order' })
  expect(within(fixtures).getByText('Amber wins')).toBeInTheDocument()
  expect(within(fixtures).getByText('Retried')).toBeInTheDocument()
  expect(within(fixtures).getByRole('link', { name: 'Open battle →' })).toHaveAttribute('href', `/battles/${battleID}`)
  expect(screen.getByRole('link', { name: 'STATISTICS →' })).toHaveAttribute('href', '/analytics?leagueId=league-1')
  expect(screen.queryByRole('button', { name: 'CANCEL LEAGUE' })).not.toBeInTheDocument()
})

it('names the champion of a completed league', async () => {
  vi.mocked(fetch).mockImplementation(async () => reply(200, sampleLeague({ status: 'completed', champion: 3 })))
  show()
  expect(await screen.findByText(/Champion:/)).toHaveTextContent('Champion: Dusk')
  expect(screen.queryByRole('button', { name: 'CANCEL LEAGUE' })).not.toBeInTheDocument()
})

it('cancels only after confirmation and reports failures', async () => {
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    if (init?.method === 'POST') return String(input).includes('fail') ? reply(500, {}) : reply(200, sampleLeague({ status: 'cancelled' }))
    return reply(200, sampleLeague())
  })
  const user = userEvent.setup()
  show()
  await user.click(await screen.findByRole('button', { name: 'CANCEL LEAGUE' }))
  await user.click(screen.getByRole('button', { name: 'KEEP RUNNING' }))
  await user.click(screen.getByRole('button', { name: 'CANCEL LEAGUE' }))
  await user.click(screen.getByRole('button', { name: 'CONFIRM CANCEL' }))
  expect(await screen.findByText(/LEAGUE · CANCELLED/)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /CANCEL/ })).not.toBeInTheDocument()
})

it('shows a cancellation failure', async () => {
  vi.mocked(fetch).mockImplementation(async (_input, init) => init?.method === 'POST' ? reply(409, {}) : reply(200, sampleLeague()))
  const user = userEvent.setup()
  show()
  await user.click(await screen.findByRole('button', { name: 'CANCEL LEAGUE' }))
  await user.click(screen.getByRole('button', { name: 'CONFIRM CANCEL' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('already finished')
})

it('refreshes while running', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  vi.mocked(fetch).mockImplementation(async () => reply(200, sampleLeague()))
  show()
  await screen.findByRole('heading', { name: 'Autumn cup' })
  await act(async () => { await vi.advanceTimersByTimeAsync(leagueRefreshMs) })
  expect(vi.mocked(fetch).mock.calls.length).toBeGreaterThanOrEqual(2)
})

it('offers a retry when the league cannot be loaded', async () => {
  vi.mocked(fetch).mockResolvedValueOnce(reply(404, {})).mockImplementation(async () => reply(200, sampleLeague()))
  const user = userEvent.setup()
  show()
  expect(await screen.findByRole('alert')).toHaveTextContent('could not be found')
  await user.click(screen.getByRole('button', { name: 'RETRY' }))
  expect(await screen.findByRole('heading', { name: 'Autumn cup' })).toBeInTheDocument()
})
