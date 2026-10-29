import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import { CreateLeaguePage } from './CreateLeaguePage'

const leagueID = '9d7a1d2c-6c43-4cf8-9a6e-0c5a1f3b2e10'
const factions = (count: number) => Array.from({ length: count }, (_, index) => ({ id: `faction-${index}`, name: `Faction ${index}`, doctrine: '' }))
const scenarios = [
  { id: 'frontier-siege', rulesVersion: '9', tickDuration: '1s', decisionIntervalTicks: 10, rulesetId: 'r', maximumTicks: 800, mapWidth: 30, mapHeight: 20 },
  { id: 'castle-siege', rulesVersion: '9', tickDuration: '1s', decisionIntervalTicks: 10, rulesetId: 'r', maximumTicks: 600, mapWidth: 20, mapHeight: 14 },
]
const profiles = [{ id: 'claude', displayName: 'Claude', modelLabel: 'sonnet', runtime: 'cli', available: true, modelOptions: [{ id: 'sonnet', reasoningEfforts: ['low', 'high'] }] }]
const reply = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })
const model = (effort: string) => ({ type: 'model', profileId: 'claude', provider: 'anthropic', runtime: 'cli', modelIdentifier: 'sonnet', reasoningEffort: effort })
// Groups AABBAABB, Claude at high effort on the even entrants, and every setting off its default.
function sourceLeague(change: Record<string, unknown> = {}) {
  return {
    id: 'source-1', name: 'Spring cup', status: 'completed', groupCount: 2,
    settings: { scenarioIds: ['castle-siege'], tickCount: 400, tickDuration: '2s', decisionIntervalTicks: 20 },
    entrants: Array.from({ length: 8 }, (_, index) => ({ index, group: index % 4 < 2 ? 'A' : 'B', factionId: `faction-${8 - index}`, factionName: `Faction ${8 - index}`,
      controller: index % 2 ? { type: 'scripted' } : model('high') })).reverse(),
    ...change,
  }
}

function serve(factionCount = 9, create: () => Response = () => reply(201, { id: leagueID }), scheduling: unknown = { battleSlots: 4, cliSlots: 4 }, source: () => Response = () => reply(200, sourceLeague())) {
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    const url = String(input)
    if (init?.method === 'POST') return create()
    if (url.endsWith('/v1/leagues/source-1')) return source()
    if (url.endsWith('/v1/factions')) return reply(200, { factions: factions(factionCount) })
    if (url.endsWith('/v1/scenarios')) return reply(200, { scenarios })
    if (url.endsWith('/v1/model-profiles')) return reply(200, { profiles })
    if (url.endsWith('/v1/leagues')) return scheduling ? reply(200, { leagues: [], scheduling }) : reply(500, {})
    throw new Error(`unexpected ${url}`)
  })
}

function show(path = '/leagues/new') {
  return render(<MemoryRouter initialEntries={[path]}><Routes>
    <Route path="/leagues/new" element={<CreateLeaguePage />} />
    <Route path="/leagues/:id" element={<h1>League page</h1>} />
  </Routes></MemoryRouter>)
}

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn())
  vi.spyOn(crypto, 'randomUUID').mockReturnValue(leagueID)
})

it('starts a league with every entrant and the chosen settings', async () => {
  serve()
  const user = userEvent.setup()
  show()
  const start = await screen.findByRole('button', { name: /START LEAGUE/ })
  expect(start).toBeDisabled()
  expect(screen.getByText('Name the league (up to 80 characters).')).toBeInTheDocument()
  await user.type(screen.getByLabelText('League name'), 'Autumn cup')
  expect(screen.getByRole('radio', { name: /16 entrants/ })).toBeDisabled()
  await user.selectOptions(screen.getByLabelText('Controller', { selector: '#entrant-1-controller' }), 'claude')
  const estimate = screen.getByRole('region', { name: 'League estimate' })
  expect(within(estimate).getByText('30')).toBeInTheDocument()
  // One model entrant can play six group battles and four knockout legs, 51 decisions each.
  expect(within(estimate).getByText('up to 510')).toBeInTheDocument()
  expect(within(estimate).getByText(/^Up to 4 league battles run at once\./)).toBeInTheDocument()
  await user.click(start)
  expect(await screen.findByRole('heading', { name: 'League page' })).toBeInTheDocument()
  const body = JSON.parse(String(vi.mocked(fetch).mock.calls.find(([, init]) => init?.method === 'POST')?.[1]?.body))
  expect(body).toMatchObject({ id: leagueID, name: 'Autumn cup', scenarioIds: ['frontier-siege'], tickCount: 500 })
  expect(body.entrants).toHaveLength(8)
  expect(body.entrants.map((entrant: { group: string }) => entrant.group).join('')).toBe('ABABABAB')
  expect(body.entrants[1]).toEqual({ factionId: 'faction-1', group: 'B', controller: { type: 'model', profileId: 'claude', modelIdentifier: 'sonnet', reasoningEffort: 'low' } })
  expect(body.entrants[0].controller).toEqual({ type: 'scripted' })
})

it('explains what blocks a league', async () => {
  serve()
  const user = userEvent.setup()
  show()
  await user.type(await screen.findByLabelText('League name'), 'Cup')
  await user.selectOptions(screen.getByLabelText('Group', { selector: '#entrant-0-group' }), 'B')
  expect(screen.getByText('Put exactly four entrants in each group.')).toBeInTheDocument()
  await user.selectOptions(screen.getByLabelText('Group', { selector: '#entrant-0-group' }), 'A')
  await user.selectOptions(screen.getByLabelText('Faction', { selector: '#entrant-0-faction' }), '')
  expect(screen.getByText('Give every entrant a different faction.')).toBeInTheDocument()
  await user.selectOptions(screen.getByLabelText('Faction', { selector: '#entrant-0-faction' }), 'faction-8')
  await user.click(screen.getByRole('checkbox', { name: /frontier-siege/ }))
  expect(screen.getByText('Choose at least one map.')).toBeInTheDocument()
  await user.click(screen.getByRole('checkbox', { name: /castle-siege/ }))
  await user.clear(screen.getByLabelText('Maximum ticks per battle'))
  await user.type(screen.getByLabelText('Maximum ticks per battle'), '700')
  expect(screen.getByText('Enter a whole number from 1 to 600.')).toBeInTheDocument()
  await user.clear(screen.getByLabelText('Maximum ticks per battle'))
  await user.type(screen.getByLabelText('Maximum ticks per battle'), '600')
  await user.selectOptions(screen.getByLabelText('Decision interval'), '5')
  await user.selectOptions(screen.getByLabelText('Tick duration'), '2s')
  expect(screen.getByRole('button', { name: /START LEAGUE/ })).toBeEnabled()
})

it('warns about long battles and shows rejected leagues', async () => {
  serve(16, () => reply(422, {}))
  const user = userEvent.setup()
  show()
  await user.type(await screen.findByLabelText('League name'), 'Big cup')
  await user.click(screen.getByRole('radio', { name: /16 entrants/ }))
  const rows = screen.getAllByRole('listitem').filter(item => item.className === 'league-entrant')
  expect(rows).toHaveLength(16)
  // Setup only, scoped per row to keep label lookups cheap; the first test drives a
  // controller choice through userEvent.
  for (const row of rows) fireEvent.change(within(row).getByLabelText('Controller'), { target: { value: 'claude' } })
  await user.selectOptions(screen.getByLabelText('Tick duration'), '2s')
  expect(screen.getByText(/approach the one-hour battle timeout/)).toBeInTheDocument()
  expect(screen.getByText(/or 2 when every battle has two CLI models/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: /START LEAGUE/ }))
  expect(await screen.findByText(/model is unavailable/)).toBeInTheDocument()
})

it('assumes one battle at a time when the capacity is unknown', async () => {
  serve(9, undefined, null)
  show()
  expect(await screen.findByText(/^League battles run one at a time\./)).toBeInTheDocument()
})

it('asks for eight factions first', async () => {
  serve(5)
  show()
  expect(await screen.findByRole('heading', { name: 'Eight factions are required' })).toBeInTheDocument()
})

it('offers a retry when setup data cannot be loaded', async () => {
  vi.mocked(fetch).mockResolvedValueOnce(reply(500, {})).mockResolvedValueOnce(reply(200, { scenarios }))
  const user = userEvent.setup()
  show()
  expect(await screen.findByRole('alert')).toHaveTextContent('League setup unavailable')
  serve()
  await user.click(screen.getByRole('button', { name: 'RETRY' }))
  expect(await screen.findByLabelText('League name')).toBeInTheDocument()
})

it('starts a new league from a copy of another league', async () => {
  serve()
  const user = userEvent.setup()
  show('/leagues/new?from=source-1')
  expect(await screen.findByLabelText('League name')).toHaveValue('Spring cup')
  expect(screen.getByRole('link', { name: 'Spring cup' })).toHaveAttribute('href', '/leagues/source-1')
  expect(screen.getByRole('checkbox', { name: /castle-siege/ })).toBeChecked()
  expect(screen.getByRole('checkbox', { name: /frontier-siege/ })).not.toBeChecked()
  expect(screen.getByLabelText('Maximum ticks per battle')).toHaveValue(400)
  expect(screen.getByLabelText('Tick duration')).toHaveValue('2s')
  expect(screen.getByLabelText('Decision interval')).toHaveValue('20')
  expect(screen.getByLabelText('Faction', { selector: '#entrant-0-faction' })).toHaveValue('faction-8')
  expect(screen.getByLabelText('Reasoning effort', { selector: '#entrant-0-controller-effort' })).toHaveValue('high')
  await user.selectOptions(screen.getByLabelText('Faction', { selector: '#entrant-7-faction' }), 'faction-0')
  await user.click(screen.getByRole('button', { name: /START LEAGUE/ }))
  expect(await screen.findByRole('heading', { name: 'League page' })).toBeInTheDocument()
  const body = JSON.parse(String(vi.mocked(fetch).mock.calls.find(([, init]) => init?.method === 'POST')?.[1]?.body))
  expect(body).toEqual({
    id: leagueID, name: 'Spring cup', scenarioIds: ['castle-siege'], tickCount: 400, tickDuration: '2s', decisionIntervalTicks: 20,
    entrants: Array.from({ length: 8 }, (_, index) => ({ factionId: index === 7 ? 'faction-0' : `faction-${8 - index}`, group: index % 4 < 2 ? 'A' : 'B',
      controller: index % 2 ? { type: 'scripted' } : { type: 'model', profileId: 'claude', modelIdentifier: 'sonnet', reasoningEffort: 'high' } })),
  })
})

it('leaves copied factions, maps and models that no longer exist for the blockers', async () => {
  const entrants = sourceLeague().entrants.map(entrant => entrant.index === 0 ? { ...entrant, factionId: 'deleted', controller: { ...model('low'), profileId: 'retired' } } : entrant)
  serve(9, undefined, undefined, () => reply(200, sourceLeague({ entrants, settings: { scenarioIds: ['retired-map'], tickCount: 400, tickDuration: '750ms' } })))
  show('/leagues/new?from=source-1')
  expect(await screen.findByLabelText('Faction', { selector: '#entrant-0-faction' })).toHaveValue('')
  expect(screen.getByLabelText('Controller', { selector: '#entrant-0-controller' })).toHaveDisplayValue('Unavailable controller')
  expect(screen.getByText('Selected controller is unavailable. Choose an available controller.')).toBeInTheDocument()
  expect(screen.getByLabelText('Tick duration')).toHaveValue('750ms')
  expect(screen.getByLabelText('Decision interval')).toHaveValue('')
  for (const blocker of ['Choose at least one map.', 'Give every entrant a different faction.', 'Choose an available controller for every entrant.']) {
    expect(screen.getByText(blocker)).toBeInTheDocument()
  }
  expect(screen.getByRole('button', { name: /START LEAGUE/ })).toBeDisabled()
})

it('offers a retry when the league to copy cannot be loaded', async () => {
  serve(9, undefined, undefined, () => reply(404, {}))
  const user = userEvent.setup()
  show('/leagues/new?from=source-1')
  expect(await screen.findByRole('alert')).toHaveTextContent('the league to copy could not be loaded')
  serve()
  await user.click(screen.getByRole('button', { name: 'RETRY' }))
  expect(await screen.findByLabelText('League name')).toHaveValue('Spring cup')
})
