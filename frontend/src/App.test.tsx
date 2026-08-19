import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'

const firstId = '7f22e693-bacd-46bb-b942-64fa613cd388'
const secondId = '4db8d152-1e04-4d10-8824-859c6ed72878'

describe('App', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
    vi.spyOn(crypto, 'randomUUID')
      .mockReturnValueOnce(firstId)
      .mockReturnValue(secondId)
  })

  it('creates a faction and adds it to the active roster', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 201 }))
    const user = userEvent.setup()
    render(<App />)

    await user.type(screen.getByLabelText('Faction name'), '  The Verdant Accord  ')
    await user.click(screen.getByRole('button', { name: 'CREATE FACTION' }))

    expect(fetch).toHaveBeenCalledWith('/v1/factions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: firstId, name: 'The Verdant Accord' }),
    })
    expect(await screen.findByText('The Verdant Accord has entered the battlefield.')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Recently deployed' })).toBeInTheDocument()
  })

  it('shows a useful conflict error from the API', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 409 }))
    const user = userEvent.setup()
    render(<App />)

    await user.type(screen.getByLabelText('Faction name'), 'Northwind')
    await user.click(screen.getByRole('button', { name: 'CREATE FACTION' }))

    expect(await screen.findByText(/identifier is already in use/i)).toBeInTheDocument()
  })
})

