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
    settings: { scenarioIds: ['frontier-siege'], tickCount: 500 }, champion: null, progress: { settled: 1, total: 13 }, rulesVersions: ['13'],
    // Gale has lost two group matches, so it alone can no longer win.
    entrants: names.map((factionName, index) => ({ index, group: index % 2 ? 'B' : 'A', factionId: `f${index}`, factionName,
      controller: index === 0 ? { type: 'model', modelIdentifier: 'sonnet', reasoningEffort: 'low' } : { type: 'scripted' }, eliminated: index === 6, disqualification: null })),
    // Group A has placed everyone; group B has not played.
    groups: [
      { group: 'A', standings: [[0, 1, 2, 0], [2, 2, 2, 1], [4, 3, 1, 2], [6, 4, 0, 2]].map(([entrant, placement, won, lost]) => ({ entrant, placement, won, lost })) },
      { group: 'B', standings: [1, 3, 5, 7].map(entrant => ({ entrant, placement: null, won: 0, lost: 0 })) },
    ],
    knockout: [{ stage: 'semifinal', tie: 1, homeEntrant: 0, awayEntrant: 3, winner: 0 }],
    fixtures: [
      { id: 'x1', sequence: 0, stage: 'group', group: 'A', round: 1, tie: 1, match: 'opening', homeEntrant: 0, awayEntrant: 6, scenarioId: 'frontier-siege', status: 'completed', attempts: 2, battleId: battleID, result: { winner: 0, reason: 'castle_destroyed' }, winner: 0 },
      { id: 'x2', sequence: 1, stage: 'group', group: 'B', round: 1, tie: 1, match: 'opening', homeEntrant: 1, awayEntrant: 7, scenarioId: 'frontier-siege', status: 'pending', attempts: 0, battleId: null, winner: null },
    ],
    createdAt: '2026-10-28T00:00:00Z', completedAt: null, ...change,
  }
}

const reply = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

function show(canManage = true) {
  return render(<MemoryRouter initialEntries={['/leagues/league-1']}><Routes><Route path="/leagues/:id" element={<LeaguePage canManage={canManage} />} /></Routes></MemoryRouter>)
}

beforeEach(() => { vi.stubGlobal('fetch', vi.fn()) })
afterEach(() => { vi.useRealTimers() })

it('shows group tables and matches, the bracket and fixtures', async () => {
  vi.mocked(fetch).mockImplementation(async () => reply(200, sampleLeague()))
  show(false)
  expect(await screen.findByRole('heading', { name: 'Autumn cup' })).toBeInTheDocument()
  expect(screen.getByText(/ticks per battle/)).toHaveTextContent('500 ticks per battle · rules v13 · 1 of 13 fixtures settled')
  const rows = within(screen.getByRole('table', { name: 'Group A standings' })).getAllByRole('row')
  expect(rows[1]).toHaveTextContent(/^1stAmbersonnet · low20$/)
  expect(rows[2]).toHaveTextContent(/^2ndCinder/)
  expect(rows[2]).toHaveClass('league-table__qualifier')
  expect(rows[3]).not.toHaveClass('league-table__qualifier')
  const groupB = within(screen.getByRole('table', { name: 'Group B standings' })).getAllByRole('row')
  expect(groupB[1]).toHaveTextContent(/^–Basalt/)
  expect(groupB[1]).not.toHaveClass('league-table__qualifier')
  const matches = screen.getByRole('list', { name: 'Group A matches' })
  expect(within(matches).getByRole('list', { name: 'Group A Opening match 1' })).toHaveTextContent(/^Amber.*\(winner\)Gale/)
  expect(within(matches).getByText('Decider')).toBeInTheDocument()
  expect(within(matches).getAllByText('To be decided')).toHaveLength(4)
  const bracket = screen.getByRole('region', { name: 'Knockout bracket' })
  expect(within(bracket).getByRole('list', { name: 'Semifinals tie 1' })).toHaveTextContent(/^Amber.*\(winner\)Dusk/)
  expect(within(bracket).getAllByText('To be decided')).toHaveLength(2)
  expect(within(bracket).getAllByText('ONE BATTLE')).toHaveLength(2)
  const fixtures = screen.getByRole('table', { name: 'Fixtures in play order' })
  expect(within(fixtures).getByText('Amber wins')).toBeInTheDocument()
  expect(within(fixtures).getAllByRole('columnheader').map(header => header.textContent)).toEqual(['#', 'Fixture', 'Side 1', 'Side 2', 'Map', 'Result', 'Battle'])
  const [, , side1, side2] = within(within(fixtures).getAllByRole('row')[1]).getAllByRole('cell')
  expect(side1).toHaveTextContent(/^Amber\s*sonnet · low$/)
  expect(side2).toHaveTextContent(/^Gale\s*Scripted$/)
  expect(within(fixtures).getByText('Retried')).toBeInTheDocument()
  expect(within(fixtures).getByRole('link', { name: 'Open battle →' })).toHaveAttribute('href', `/battles/${battleID}`)
  expect(screen.getByRole('link', { name: 'STATISTICS →' })).toHaveAttribute('href', '/analytics?leagueId=league-1')
  expect(screen.queryByRole('button', { name: 'CANCEL LEAGUE' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'DISQUALIFY ENTRANT' })).not.toBeInTheDocument()
  expect(screen.queryByRole('link', { name: 'START AGAIN →' })).not.toBeInTheDocument()
  expect(screen.queryByRole('region', { name: 'Now playing' })).not.toBeInTheDocument()
  expect(screen.queryByRole('region', { name: 'Disqualifications' })).not.toBeInTheDocument()
})

it('puts running battles first with their tick progress', async () => {
  const running = '22222222-2222-4222-8222-222222222222'
  const league = sampleLeague()
  league.fixtures.push(
    { id: 'x3', sequence: 2, stage: 'group', group: 'A', round: 2, tie: 1, match: 'winners', homeEntrant: 0, awayEntrant: 2, scenarioId: 'castle-siege', status: 'running', attempts: 1, battleId: running, battleProgress: { currentTick: 120, requestedTicks: 500 }, winner: null },
    { id: 'x4', sequence: 3, stage: 'group', group: 'B', round: 2, tie: 1, match: 'winners', homeEntrant: 1, awayEntrant: 3, scenarioId: 'frontier-siege', status: 'running', attempts: 1, battleId: null, winner: null },
  )
  vi.mocked(fetch).mockImplementation(async () => reply(200, league))
  show(false)
  const live = await screen.findByRole('region', { name: 'Now playing' })
  expect(live).toHaveTextContent('2 RUNNING')
  const [first, second] = within(live).getAllByRole('listitem')
  expect(first).toHaveTextContent(/Group A · Winners' match · castle-siege.*Amber.*sonnet · low.*Cinder.*Scripted/)
  expect(within(first).getByRole('progressbar', { name: "Group A · Winners' match ticks played" })).toHaveValue(120)
  expect(first).toHaveTextContent('Tick 120 of 500')
  expect(within(first).getByRole('link', { name: 'WATCH LIVE →' })).toHaveAttribute('href', `/battles/${running}`)
  expect(second).toHaveTextContent('Starting…')
  expect(within(second).queryByRole('link')).not.toBeInTheDocument()

  const rows = within(screen.getByRole('table', { name: 'Fixtures in play order' })).getAllByRole('row')
  expect(rows[3]).toHaveClass('league-fixtures__running')
  expect(rows[3]).toHaveTextContent('Tick 120 of 500')
  expect(rows[2]).not.toHaveClass('league-fixtures__running')
})

it('names the champion of a completed league and offers Admins a copy of its setup', async () => {
  vi.mocked(fetch).mockImplementation(async () => reply(200, sampleLeague({ status: 'completed', champion: 3 })))
  show()
  expect(await screen.findByText(/Champion:/)).toHaveTextContent('Champion: Dusk')
  expect(screen.queryByRole('button', { name: 'CANCEL LEAGUE' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'DISQUALIFY ENTRANT' })).not.toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'START AGAIN →' })).toHaveAttribute('href', '/leagues/new?from=league-1')
})

it('names every rules version a league spans and none before its first battle', async () => {
  vi.mocked(fetch).mockResolvedValueOnce(reply(200, sampleLeague({ status: 'completed', rulesVersions: ['13', '12'] })))
  const view = show()
  expect(await screen.findByText(/ticks per battle/)).toHaveTextContent('· rules v13, v12 ·')
  view.unmount()
  vi.mocked(fetch).mockResolvedValueOnce(reply(200, sampleLeague({ status: 'completed', rulesVersions: [] })))
  show()
  expect(await screen.findByText(/ticks per battle/)).not.toHaveTextContent('rules')
})

it('disqualifies a picked entrant after confirmation and records who did it and why', async () => {
  const disqualified = sampleLeague()
  disqualified.entrants[2] = { ...disqualified.entrants[2], disqualification: { at: '2026-10-30T01:00:00Z', by: 'admin-1', reason: 'Too slow' } }
  disqualified.fixtures[0] = { ...disqualified.fixtures[0], status: 'forfeited', forfeit: 'away', result: undefined, winner: 0 }
  vi.mocked(fetch).mockImplementation(async (_input, init) => reply(200, init?.method === 'POST' ? disqualified : sampleLeague()))
  const user = userEvent.setup()
  show()
  const open = await screen.findByRole('button', { name: 'DISQUALIFY ENTRANT' })
  expect(within(screen.getByRole('table', { name: 'Group A standings' })).queryByRole('button')).not.toBeInTheDocument()
  expect(open).toHaveAttribute('aria-expanded', 'false')
  await user.click(open)
  expect(open).toHaveAttribute('aria-expanded', 'true')
  await user.click(screen.getByRole('button', { name: 'KEEP IN LEAGUE' }))
  expect(screen.queryByRole('form', { name: 'Disqualify an entrant' })).not.toBeInTheDocument()
  await user.click(open)
  const form = screen.getByRole('form', { name: 'Disqualify an entrant' })
  const picker = within(form).getByRole('combobox', { name: 'Entrant' })
  // Gale can no longer win, so it is not offered.
  expect(within(picker).getAllByRole('option').map(option => option.textContent)).toEqual([
    'Choose an entrant', 'Amber (sonnet · low)', 'Cinder (Scripted)', 'Ember (Scripted)',
    'Basalt (Scripted)', 'Dusk (Scripted)', 'Frost (Scripted)', 'Haze (Scripted)',
  ])
  expect(form).toHaveTextContent('Choose the entrant to disqualify.')
  expect(within(form).getByRole('button', { name: 'CONFIRM DISQUALIFY' })).toBeDisabled()
  await user.selectOptions(picker, 'Cinder (Scripted)')
  expect(form).toHaveTextContent('Disqualify Cinder? It loses the match it is playing')
  await user.type(within(form).getByRole('textbox', { name: 'Reason (optional)' }), ' Too slow ')
  await user.click(within(form).getByRole('button', { name: 'CONFIRM DISQUALIFY' }))
  const record = await screen.findByRole('region', { name: 'Disqualifications' })
  const [url, init] = vi.mocked(fetch).mock.calls.find(([, request]) => request?.method === 'POST') ?? []
  expect(String(url)).toContain('/v1/leagues/league-1/entrants/2/disqualify')
  expect(init?.body).toBe(JSON.stringify({ reason: 'Too slow' }))
  expect(record).toHaveTextContent(/Cinder by admin-1 · .+ · Too slow/)
  expect(screen.queryByRole('form', { name: 'Disqualify an entrant' })).not.toBeInTheDocument()
  expect(open).toHaveAttribute('aria-expanded', 'false')
  const cinder = within(screen.getByRole('table', { name: 'Group A standings' })).getAllByRole('row')[2]
  expect(cinder).toHaveClass('league-table__disqualified')
  expect(cinder).toHaveTextContent('(disqualified)')
  await user.click(open)
  expect(within(screen.getByRole('combobox', { name: 'Entrant' })).queryByRole('option', { name: 'Cinder (Scripted)' })).not.toBeInTheDocument()
  expect(within(screen.getByRole('table', { name: 'Fixtures in play order' })).getByText('Forfeit · Amber wins')).toBeInTheDocument()
})

it('shows why a disqualification was refused or failed', async () => {
  const refusals = [reply(409, { error: { message: 'entrant is the last one who can still win; cancel the league instead' } }), new Response('not json', { status: 200 })]
  vi.mocked(fetch).mockImplementation(async (_input, init) => init?.method === 'POST' ? refusals.shift() ?? reply(500, {}) : reply(200, sampleLeague()))
  const user = userEvent.setup()
  show()
  await user.click(await screen.findByRole('button', { name: 'DISQUALIFY ENTRANT' }))
  const form = screen.getByRole('form', { name: 'Disqualify an entrant' })
  await user.selectOptions(within(form).getByRole('combobox', { name: 'Entrant' }), 'Basalt (Scripted)')
  await user.click(within(form).getByRole('button', { name: 'CONFIRM DISQUALIFY' }))
  expect(await within(form).findByRole('alert')).toHaveTextContent('cannot be disqualified: entrant is the last one who can still win')
  await user.click(within(form).getByRole('button', { name: 'CONFIRM DISQUALIFY' }))
  expect(await within(form).findByText('The entrant could not be disqualified.')).toBeInTheDocument()
  expect(within(form).getByRole('button', { name: 'CONFIRM DISQUALIFY' })).toBeEnabled()
  await user.selectOptions(within(form).getByRole('combobox', { name: 'Entrant' }), 'Dusk (Scripted)')
  expect(within(form).queryByRole('alert')).not.toBeInTheDocument()
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

it('refreshes while running and keeps the last standings when a refresh fails', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  vi.mocked(fetch).mockResolvedValueOnce(reply(200, sampleLeague())).mockResolvedValue(reply(503, {}))
  show()
  await screen.findByRole('heading', { name: 'Autumn cup' })
  await act(async () => { await vi.advanceTimersByTimeAsync(leagueRefreshMs) })
  expect(vi.mocked(fetch).mock.calls.length).toBeGreaterThanOrEqual(2)
  expect(await screen.findByText(/^Refresh failed:/)).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: 'Autumn cup' })).toBeInTheDocument()
})

it('offers a retry when the league cannot be loaded', async () => {
  vi.mocked(fetch).mockResolvedValueOnce(reply(404, {})).mockImplementation(async () => reply(200, sampleLeague()))
  const user = userEvent.setup()
  show()
  expect(await screen.findByRole('alert')).toHaveTextContent('could not be found')
  await user.click(screen.getByRole('button', { name: 'RETRY' }))
  expect(await screen.findByRole('heading', { name: 'Autumn cup' })).toBeInTheDocument()
})
