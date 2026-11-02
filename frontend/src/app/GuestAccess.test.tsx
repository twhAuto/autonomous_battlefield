// Checks the Guest experience: a view-only label, leagues and statistics to browse, no
// routes or links that start, create or manage anything, and the Admin preview of it.

import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import App from './App'
import type { AuthSession } from '../features/auth/api/auth'

const guest: AuthSession = { enabled: true, authenticated: true, mode: 'cloudflare', actor: { id: 'visitor', role: 'guest' } }
const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200 })

function renderApp(path: string, session: AuthSession = guest) {
  return render(<MemoryRouter initialEntries={[path]}><App initialSession={session} /></MemoryRouter>)
}

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.startsWith('/v1/factions')) return json({ factions: [{ id: 'mine', name: 'Northwind', doctrine: 'Hold.', background: '', ownerId: 'visitor' }] })
    if (url.startsWith('/v1/battles')) return json({ battles: [], nextCursor: null })
    if (url.startsWith('/v1/leagues')) return json({ leagues: [] })
    throw new Error(`Unexpected fetch: ${url}`)
  }))
})

it('labels a Guest session view-only and offers only viewing destinations', () => {
  renderApp('/')
  expect(screen.getByText(/Guest access · View only/)).toBeInTheDocument()
  const menu = screen.getByRole('region', { name: 'Main menu' })
  expect(within(menu).queryByRole('link', { name: /stage battle/i })).not.toBeInTheDocument()
  expect(within(menu).getAllByRole('link').map(link => link.getAttribute('href'))).toEqual(['/factions', '/battles', '/leagues', '/analytics'])
  expect(within(menu).getByText('Browse the factions and their doctrines.')).toBeInTheDocument()
  const navigation = screen.getByRole('navigation', { name: 'Primary navigation' })
  expect(within(navigation).queryByRole('link', { name: 'Stage battle' })).not.toBeInTheDocument()
})

it.each(['/battles/new', '/factions/new', '/factions/mine/edit', '/leagues/new'])('does not serve %s to a Guest', (path) => {
  renderApp(path)
  expect(screen.getByRole('heading', { name: 'This route is outside the mapped frontier.' })).toBeInTheDocument()
})

it('shows a Guest factions without create or edit links, even its own', async () => {
  renderApp('/factions')
  expect(await screen.findByText('Northwind')).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: /create faction/i })).not.toBeInTheDocument()
  expect(screen.queryByRole('link', { name: 'Edit faction' })).not.toBeInTheDocument()
})

it('shows a Guest the leagues without a way to start one', async () => {
  renderApp('/leagues')
  expect(await screen.findByText('An administrator can start a league.')).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: /start league/i })).not.toBeInTheDocument()
})

it('shows a Guest an empty battle history without staging links', async () => {
  renderApp('/battles')
  expect(await screen.findByText('No battles have been staged yet.')).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: /stage battle/i })).not.toBeInTheDocument()
})

it('lets an Admin preview the Guest view and exit it', async () => {
  const user = userEvent.setup()
  vi.mocked(fetch).mockImplementation(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.startsWith('/v1/admin/model-access')) return json({ version: 0, policy: { providers: {}, profiles: {} }, models: [] })
    if (url.startsWith('/v1/battles')) return json({ battles: [], nextCursor: null })
    throw new Error(`Unexpected fetch: ${url}`)
  })
  renderApp('/admin', { enabled: true, authenticated: true, mode: 'cloudflare', actor: { id: 'owner', role: 'admin' } })
  await user.click(await screen.findByRole('button', { name: 'View as Guest' }))
  expect(await screen.findByText(/Viewing as Guest · Read-only preview/)).toBeInTheDocument()
  expect(screen.queryByText(/Guest access · View only/)).not.toBeInTheDocument()
  expect(await screen.findByText('No battles have been staged yet.')).toBeInTheDocument()
  expect(vi.mocked(fetch)).toHaveBeenLastCalledWith(expect.stringContaining('/v1/battles'), expect.objectContaining({ headers: expect.objectContaining({ 'X-Battlefield-Preview-Role': 'guest' }) }))
  await user.click(screen.getByRole('button', { name: 'Exit preview' }))
  expect(screen.queryByText(/Viewing as Guest/)).not.toBeInTheDocument()
})
