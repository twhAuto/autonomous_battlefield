// Checks which callers the registry offers faction creation, and the reason a Player
// who already owns a faction sees instead.

import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import { SessionContext } from '../../auth/SessionContext'
import type { AuthSession } from '../../auth/api/auth'
import { factionLimitMessage } from '../limits'
import { FactionsPage } from './FactionsPage'

const own = { id: '7f22e693-bacd-46bb-b942-64fa613cd388', name: 'Northwind', doctrine: 'Hold', ownerId: 'self' }
const other = { id: '4db8d152-1e04-4d10-8824-859c6ed72878', name: 'Verdant Accord', doctrine: 'Grow', ownerId: 'someone-else' }

function show(role: 'player' | 'tester' | 'admin') {
  const session: AuthSession = { enabled: true, authenticated: true, mode: 'cloudflare', actor: { id: 'self', role } }
  return render(<SessionContext.Provider value={session}><MemoryRouter><FactionsPage /></MemoryRouter></SessionContext.Provider>)
}

const registry = (factions: unknown[]) => new Response(JSON.stringify({ factions }))
const createLink = () => screen.queryByRole('link', { name: '+ CREATE FACTION' })

beforeEach(() => { vi.stubGlobal('fetch', vi.fn()) })

it('tells a Player who owns a faction why they cannot create another', async () => {
  vi.mocked(fetch).mockResolvedValue(registry([other, own]))
  show('player')
  expect(await screen.findByRole('note')).toHaveTextContent(factionLimitMessage)
  expect(createLink()).not.toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Edit faction' })).toHaveAttribute('href', `/factions/${own.id}/edit`)
})

it('offers a Player creation once the registry shows they own none', async () => {
  let respond!: (response: Response) => void
  vi.mocked(fetch).mockImplementation(() => new Promise(resolve => { respond = resolve }))
  show('player')
  expect(createLink()).not.toBeInTheDocument()
  respond(registry([other]))
  expect(await screen.findByRole('link', { name: '+ CREATE FACTION' })).toHaveAttribute('href', '/factions/new')
  expect(screen.queryByRole('note')).not.toBeInTheDocument()
})

it('offers a Player creation when the registry cannot be read, leaving the limit to the API', async () => {
  vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 503 }))
  show('player')
  expect(await screen.findByRole('heading', { name: 'Registry unavailable' })).toBeInTheDocument()
  expect(createLink()).toBeInTheDocument()
})

it.each(['tester', 'admin'] as const)('never limits a %s who owns factions', async (role) => {
  vi.mocked(fetch).mockResolvedValue(registry([own, { ...other, ownerId: 'self' }]))
  show(role)
  expect(createLink()).toBeInTheDocument()
  expect(await screen.findByRole('heading', { name: 'Northwind' })).toBeInTheDocument()
  expect(createLink()).toBeInTheDocument()
  expect(screen.queryByRole('note')).not.toBeInTheDocument()
})
