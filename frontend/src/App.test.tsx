import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'

const firstId = '7f22e693-bacd-46bb-b942-64fa613cd388'
const secondId = '4db8d152-1e04-4d10-8824-859c6ed72878'

function renderApp(path = '/') {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  )
}

describe('App', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
    vi.spyOn(crypto, 'randomUUID')
      .mockReturnValueOnce(firstId)
      .mockReturnValue(secondId)
  })

  it('offers both faction operations on the main menu', () => {
    renderApp()
	const menu = screen.getByRole('heading', { name: 'Main menu' }).closest('section')!

    expect(within(menu).getByRole('link', { name: /create faction/i })).toHaveAttribute('href', '/factions/new')
    expect(within(menu).getByRole('link', { name: /view all factions/i })).toHaveAttribute('href', '/factions')
  })

  it('navigates from the main menu to faction creation', async () => {
    const user = userEvent.setup()
    renderApp()
	const menu = screen.getByRole('heading', { name: 'Main menu' }).closest('section')!

    await user.click(within(menu).getByRole('link', { name: /create faction/i }))

    expect(screen.getByRole('heading', { name: 'Create a faction' })).toBeInTheDocument()
    expect(screen.getByLabelText('Faction name')).toBeInTheDocument()
  })

  it('creates a faction from its dedicated route', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 201 }))
    const user = userEvent.setup()
    renderApp('/factions/new')

    await user.type(screen.getByLabelText('Faction name'), '  The Verdant Accord  ')
    await user.click(screen.getByRole('button', { name: 'CREATE FACTION' }))

    expect(fetch).toHaveBeenCalledWith('/v1/factions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: firstId, name: 'The Verdant Accord' }),
    })
    expect(await screen.findByText('The Verdant Accord has entered the battlefield.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /view faction registry/i })).toHaveAttribute('href', '/factions')
  })

  it('renders every faction returned by the registry', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify({ factions: [
      { id: firstId, name: 'Northwind' },
      { id: secondId, name: 'Verdant Accord' },
    ] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))

    renderApp('/factions')

    expect(await screen.findByRole('heading', { name: 'Northwind' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Verdant Accord' })).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/v1/factions', expect.objectContaining({ signal: expect.any(AbortSignal) }))
  })

  it('shows an empty registry with a create action', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify({ factions: [] }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))

    renderApp('/factions')

    expect(await screen.findByRole('heading', { name: 'The frontier is unclaimed' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /create first faction/i })).toHaveAttribute('href', '/factions/new')
  })

  it('shows a retryable registry error', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 500 }))

    renderApp('/factions')

    expect(await screen.findByRole('heading', { name: 'Registry unavailable' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'RETRY CONNECTION' })).toBeInTheDocument()
  })
})
