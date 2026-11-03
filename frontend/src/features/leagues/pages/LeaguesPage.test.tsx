// Checks the league list: progress, champions and rules versions, the rules-version
// filter, empty states and retry.

import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import { LeaguesPage } from './LeaguesPage'

const reply = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })
const summary = { id: 'league-1', ownerId: 'admin', name: 'Autumn cup', status: 'completed', groupCount: 2, entrantCount: 8,
  champion: { entrant: 3, factionName: 'Dusk' }, progress: { settled: 13, total: 13 }, rulesVersions: ['13', '12'], createdAt: '2026-10-28T00:00:00Z', completedAt: '2026-10-29T00:00:00Z' }

// The page and its rules-version filter fetch separately; leagues answers the league list.
function serveLeagues(leagues: (url: string) => Response) {
  vi.mocked(fetch).mockImplementation(async (input) => String(input).startsWith('/v1/battles/rules-versions')
    ? reply(200, { current: '13', versions: ['13', '12'] })
    : leagues(String(input)))
}
function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location">{location.pathname + location.search}</output>
}

beforeEach(() => { vi.stubGlobal('fetch', vi.fn()) })

it('lists leagues with progress, champions and rules versions', async () => {
  serveLeagues(() => reply(200, { leagues: [summary] }))
  render(<MemoryRouter><LeaguesPage canCreate /></MemoryRouter>)
  expect(await screen.findByText('Autumn cup')).toBeInTheDocument()
  expect(screen.getByText('13 / 13 fixtures')).toBeInTheDocument()
  expect(screen.getByText('Dusk')).toBeInTheDocument()
  expect(screen.getByText('v13, v12')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Open league →' })).toHaveAttribute('href', '/leagues/league-1')
  expect(screen.getByRole('link', { name: '+ START LEAGUE' })).toBeInTheDocument()
})

it('explains an empty list to people who cannot start a league', async () => {
  serveLeagues(() => reply(200, { leagues: [] }))
  render(<MemoryRouter><LeaguesPage canCreate={false} /></MemoryRouter>)
  expect(await screen.findByText('An administrator can start a league.')).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: /START LEAGUE/ })).not.toBeInTheDocument()
})

it('retries a failed load', async () => {
  const replies = [reply(500, {}), reply(200, { leagues: [] })]
  serveLeagues(() => replies.shift()!)
  const user = userEvent.setup()
  render(<MemoryRouter><LeaguesPage canCreate /></MemoryRouter>)
  expect(await screen.findByRole('alert')).toHaveTextContent('Leagues could not be loaded.')
  await user.click(screen.getByRole('button', { name: 'RETRY' }))
  expect(await screen.findByRole('heading', { name: 'No leagues yet' })).toBeInTheDocument()
})

it('filters leagues by rules version from the URL', async () => {
  const user = userEvent.setup()
  serveLeagues(url => reply(200, { leagues: url.includes('rulesVersion=12') ? [] : [{ ...summary, rulesVersions: [] }] }))
  render(<MemoryRouter initialEntries={['/leagues']}><LocationProbe /><LeaguesPage canCreate /></MemoryRouter>)
  expect(await screen.findByText('Autumn cup')).toBeInTheDocument()
  expect(screen.getAllByRole('cell').find(cell => cell.dataset.label === 'Rules')).toHaveTextContent('—')
  await screen.findByRole('option', { name: 'v12' })
  await user.selectOptions(screen.getByRole('combobox', { name: 'Rules' }), '12')
  expect(await screen.findByRole('heading', { name: 'No matching leagues' })).toBeInTheDocument()
  expect(screen.getByText('No league has a battle on rules v12. Choose another version.')).toBeInTheDocument()
  expect(screen.getByTestId('location')).toHaveTextContent('/leagues?rulesVersion=12')
  expect(fetch).toHaveBeenCalledWith('/v1/leagues?rulesVersion=12', expect.anything())
  await user.selectOptions(screen.getByRole('combobox', { name: 'Rules' }), '')
  await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('/leagues'))
  expect(await screen.findByText('Autumn cup')).toBeInTheDocument()
})
