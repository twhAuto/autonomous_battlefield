import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import { LeaguesPage } from './LeaguesPage'

const reply = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })
const summary = { id: 'league-1', ownerId: 'admin', name: 'Autumn cup', status: 'completed', groupCount: 2, entrantCount: 8,
  champion: { entrant: 3, factionName: 'Dusk' }, progress: { settled: 30, total: 30 }, createdAt: '2026-10-28T00:00:00Z', completedAt: '2026-10-29T00:00:00Z' }

beforeEach(() => { vi.stubGlobal('fetch', vi.fn()) })

it('lists leagues with progress and champions', async () => {
  vi.mocked(fetch).mockResolvedValue(reply(200, { leagues: [summary] }))
  render(<MemoryRouter><LeaguesPage canCreate /></MemoryRouter>)
  expect(await screen.findByText('Autumn cup')).toBeInTheDocument()
  expect(screen.getByText('30 / 30 fixtures')).toBeInTheDocument()
  expect(screen.getByText('Dusk')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Open league →' })).toHaveAttribute('href', '/leagues/league-1')
  expect(screen.getByRole('link', { name: '+ START LEAGUE' })).toBeInTheDocument()
})

it('explains an empty list to people who cannot start a league', async () => {
  vi.mocked(fetch).mockResolvedValue(reply(200, { leagues: [] }))
  render(<MemoryRouter><LeaguesPage canCreate={false} /></MemoryRouter>)
  expect(await screen.findByText('An administrator can start a league.')).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: /START LEAGUE/ })).not.toBeInTheDocument()
})

it('retries a failed load', async () => {
  vi.mocked(fetch).mockResolvedValueOnce(reply(500, {})).mockResolvedValueOnce(reply(200, { leagues: [] }))
  const user = userEvent.setup()
  render(<MemoryRouter><LeaguesPage canCreate /></MemoryRouter>)
  expect(await screen.findByRole('alert')).toHaveTextContent('Leagues could not be loaded.')
  await user.click(screen.getByRole('button', { name: 'RETRY' }))
  expect(await screen.findByRole('heading', { name: 'No leagues yet' })).toBeInTheDocument()
})
