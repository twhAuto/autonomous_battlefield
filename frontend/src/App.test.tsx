import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'

const firstId = '7f22e693-bacd-46bb-b942-64fa613cd388'
const secondId = '4db8d152-1e04-4d10-8824-859c6ed72878'
const battleId = '41ac3a2f-3371-4de1-b0f6-70600fcd9052'

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

  it('offers faction and battle operations on the main menu', () => {
    renderApp()
	const menu = screen.getByRole('heading', { name: 'Main menu' }).closest('section')!

    expect(within(menu).getByRole('link', { name: /create faction/i })).toHaveAttribute('href', '/factions/new')
    expect(within(menu).getByRole('link', { name: /view all factions/i })).toHaveAttribute('href', '/factions')
    expect(within(menu).getByRole('link', { name: /stage a battle/i })).toHaveAttribute('href', '/battles/new')
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

  it('regenerates a faction identifier', async () => {
    const user = userEvent.setup()
    renderApp('/factions/new')

    expect(screen.getByTitle(firstId)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Generate a new faction identifier' }))

    expect(screen.getByTitle(secondId)).toBeInTheDocument()
    expect(screen.queryByTitle(firstId)).not.toBeInTheDocument()
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

  it('configures a battle and lands on its live status page', async () => {
    vi.mocked(crypto.randomUUID).mockReset().mockReturnValue(battleId)
    vi.mocked(fetch).mockImplementation(async (input, init) => {
      const url = String(input)
      if (url === '/v1/factions') {
        return new Response(JSON.stringify({ factions: [
          { id: firstId, name: 'Northwind' },
          { id: secondId, name: 'Verdant Accord' },
        ] }), { status: 200 })
      }
      if (url === '/v1/scenarios') {
        return new Response(JSON.stringify({ scenarios: [{
          id: 'skirmish-v1', rulesVersion: '1', tickDuration: '0s', decisionIntervalTicks: 10, decisionLeadTicks: 2,
        }] }), { status: 200 })
      }
      if (url === '/v1/battles' && init?.method === 'POST') {
        return new Response(JSON.stringify({ id: battleId, status: 'queued' }), { status: 202 })
      }
      if (url === `/v1/battles/${battleId}`) {
        return new Response(JSON.stringify({
          id: battleId,
          scenarioId: 'skirmish-v1',
          scenarioDigest: 'digest',
          requestedTicks: 25,
          currentTick: 0,
          status: 'queued',
          attempt: 0,
          participants: [{ id: firstId, name: 'Northwind' }, { id: secondId, name: 'Verdant Accord' }],
          result: null,
        }), { status: 200 })
      }
      if (url.startsWith(`/v1/battles/${battleId}/events?`)) {
        return new Response(JSON.stringify({ attempt: 0, events: [], nextCursor: null }), { status: 200 })
      }
      throw new Error(`Unexpected fetch: ${url}`)
    })
    const user = userEvent.setup()
    renderApp('/battles/new')

    expect(await screen.findByRole('heading', { name: 'Stage a battle' })).toBeInTheDocument()
    await user.clear(screen.getByLabelText('Maximum ticks'))
    await user.type(screen.getByLabelText('Maximum ticks'), '25')
    await user.click(screen.getByRole('button', { name: 'DEPLOY COMBATANTS' }))

    expect(fetch).toHaveBeenCalledWith('/v1/battles', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        id: battleId,
        scenarioId: 'skirmish-v1',
        factionIds: [firstId, secondId],
        tickCount: 25,
      }),
    })
    expect(await screen.findByRole('heading', { name: 'Battle in progress' })).toBeInTheDocument()
    expect(screen.getByText('Awaiting engine')).toBeInTheDocument()
  })

  it('shows battle tick validation before deployment', async () => {
    vi.mocked(fetch).mockImplementation(async (input) => {
      const url = String(input)
      if (url === '/v1/factions') {
        return new Response(JSON.stringify({ factions: [
          { id: firstId, name: 'Northwind' },
          { id: secondId, name: 'Verdant Accord' },
        ] }), { status: 200 })
      }
      if (url === '/v1/scenarios') {
        return new Response(JSON.stringify({ scenarios: [{
          id: 'skirmish-v1', rulesVersion: '1', tickDuration: '0s', decisionIntervalTicks: 10, decisionLeadTicks: 2,
        }] }), { status: 200 })
      }
      throw new Error(`Unexpected fetch: ${url}`)
    })
    const user = userEvent.setup()
    renderApp('/battles/new')

    const ticks = await screen.findByLabelText('Maximum ticks')
    await user.clear(ticks)
    await user.type(ticks, '10001')

    expect(screen.getByText('Enter a whole number from 1 to 10,000.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'DEPLOY COMBATANTS' })).toBeDisabled()
  })

  it('renders a completed battle result and its event stream', async () => {
    vi.mocked(fetch).mockImplementation(async (input) => {
      const url = String(input)
      if (url === `/v1/battles/${battleId}`) {
        return new Response(JSON.stringify({
          id: battleId,
          scenarioId: 'skirmish-v1',
          scenarioDigest: 'digest',
          requestedTicks: 100,
          currentTick: 18,
          status: 'completed',
          attempt: 1,
          participants: [{ id: firstId, name: 'Northwind' }, { id: secondId, name: 'Verdant Accord' }],
          result: {
            winnerFactionId: firstId,
            reason: 'elimination',
            ticksRun: 18,
            factions: [
              { factionId: firstId, unitsStarted: 2, unitsRemaining: 1, damageDealt: 80, damageTaken: 42, attackTicks: 18, defendTicks: 0 },
              { factionId: secondId, unitsStarted: 2, unitsRemaining: 0, damageDealt: 42, damageTaken: 80, attackTicks: 10, defendTicks: 8 },
            ],
          },
        }), { status: 200 })
      }
      if (url.startsWith(`/v1/battles/${battleId}/events?`)) {
        return new Response(JSON.stringify({
          attempt: 1,
          events: [{ attempt: 1, sequence: 1, tick: 1, type: 'battle_started', data: { scenarioId: 'skirmish-v1' }, createdAt: '2026-08-21T00:00:00Z' }],
          nextCursor: 1,
        }), { status: 200 })
      }
      throw new Error(`Unexpected fetch: ${url}`)
    })

    renderApp(`/battles/${battleId}`)

    expect(await screen.findByRole('heading', { name: 'Battle complete' })).toBeInTheDocument()
    expect(screen.getByText('Northwind wins')).toBeInTheDocument()
    expect(await screen.findByText('battle started')).toBeInTheDocument()
    expect(screen.getAllByText('Damage dealt')).toHaveLength(2)
  })

  it('visibly reconnects after a transient battle status failure', async () => {
    let statusRequests = 0
    vi.mocked(fetch).mockImplementation(async (input) => {
      const url = String(input)
      if (url === `/v1/battles/${battleId}`) {
        statusRequests += 1
        if (statusRequests === 1) return new Response(null, { status: 503 })
        return new Response(JSON.stringify({
          id: battleId,
          scenarioId: 'skirmish-v1',
          scenarioDigest: 'digest',
          requestedTicks: 5,
          currentTick: 5,
          status: 'completed',
          attempt: 1,
          participants: [{ id: firstId, name: 'Northwind' }, { id: secondId, name: 'Verdant Accord' }],
          result: { winnerFactionId: null, reason: 'tick_limit', ticksRun: 5, factions: [] },
        }), { status: 200 })
      }
      if (url.startsWith(`/v1/battles/${battleId}/events?`)) {
        return new Response(JSON.stringify({ attempt: 1, events: [], nextCursor: null }), { status: 200 })
      }
      throw new Error(`Unexpected fetch: ${url}`)
    })

    renderApp(`/battles/${battleId}`)

    expect(await screen.findByText(/Reconnecting 1\/5/)).toBeInTheDocument()
    expect(await screen.findByRole('heading', { name: 'Battle complete' }, { timeout: 2500 })).toBeInTheDocument()
    expect(statusRequests).toBe(2)
  })

  it('continues the event stream from the returned sequence cursor', async () => {
    vi.mocked(fetch).mockImplementation(async (input) => {
      const url = String(input)
      if (url === `/v1/battles/${battleId}`) {
        return new Response(JSON.stringify({
          id: battleId,
          scenarioId: 'skirmish-v1',
          scenarioDigest: 'digest',
          requestedTicks: 10,
          currentTick: 10,
          status: 'completed',
          attempt: 1,
          participants: [{ id: firstId, name: 'Northwind' }, { id: secondId, name: 'Verdant Accord' }],
          result: { winnerFactionId: null, reason: 'tick_limit', ticksRun: 10, factions: [] },
        }), { status: 200 })
      }
      if (url.includes('after=0')) {
        return new Response(JSON.stringify({
          attempt: 1,
          events: Array.from({ length: 50 }, (_, index) => ({
            attempt: 1,
            sequence: index + 1,
            tick: index + 1,
            type: 'tick_completed',
            data: {},
            createdAt: '2026-08-21T00:00:00Z',
          })),
          nextCursor: 50,
        }), { status: 200 })
      }
      if (url.includes('after=50')) {
        return new Response(JSON.stringify({
          attempt: 1,
          events: [{ attempt: 1, sequence: 51, tick: 10, type: 'battle_completed', data: { reason: 'tick_limit' }, createdAt: '2026-08-21T00:00:01Z' }],
          nextCursor: 51,
        }), { status: 200 })
      }
      throw new Error(`Unexpected fetch: ${url}`)
    })
    const user = userEvent.setup()
    renderApp(`/battles/${battleId}`)

    await user.click(await screen.findByRole('button', { name: 'LOAD NEXT 50' }))

    expect(await screen.findByText('battle completed')).toBeInTheDocument()
    expect(vi.mocked(fetch).mock.calls.some(([input]) => String(input).includes('attempt=1&after=50&limit=50'))).toBe(true)
  })

  it('renders the not-found route', () => {
    renderApp('/outside-the-frontier')
    expect(screen.getByRole('heading', { name: 'This route is outside the mapped frontier.' })).toBeInTheDocument()
  })

  it('retries the faction registry after an error', async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(new Response(null, { status: 503 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ factions: [{ id: firstId, name: 'Northwind' }] }), { status: 200 }))
    const user = userEvent.setup()
    renderApp('/factions')

    await user.click(await screen.findByRole('button', { name: 'RETRY CONNECTION' }))

    expect(await screen.findByRole('heading', { name: 'Northwind' })).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('explains when too few factions are available for a battle', async () => {
    vi.mocked(fetch).mockImplementation(async (input) => {
      if (String(input) === '/v1/factions') {
        return new Response(JSON.stringify({ factions: [{ id: firstId, name: 'Northwind' }] }), { status: 200 })
      }
      return new Response(JSON.stringify({ scenarios: [{ id: 'skirmish-v1' }] }), { status: 200 })
    })

    renderApp('/battles/new')

    expect(await screen.findByRole('heading', { name: 'Two factions are required' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /create a faction/i })).toHaveAttribute('href', '/factions/new')
  })

  it('explains when no battle scenarios are available', async () => {
    vi.mocked(fetch).mockImplementation(async (input) => {
      if (String(input) === '/v1/factions') {
        return new Response(JSON.stringify({ factions: [
          { id: firstId, name: 'Northwind' },
          { id: secondId, name: 'Verdant Accord' },
        ] }), { status: 200 })
      }
      return new Response(JSON.stringify({ scenarios: [] }), { status: 200 })
    })

    renderApp('/battles/new')

    expect(await screen.findByRole('heading', { name: 'No scenarios are available' })).toBeInTheDocument()
    expect(screen.getByText(/Add a valid scenario to the server catalog/)).toBeInTheDocument()
  })

  it('renders a failed battle and complex event values', async () => {
    vi.mocked(fetch).mockImplementation(async (input) => {
      const url = String(input)
      if (url === `/v1/battles/${battleId}`) {
        return new Response(JSON.stringify({
          id: battleId,
          scenarioId: 'skirmish-v1',
          scenarioDigest: 'digest',
          requestedTicks: 10,
          currentTick: 3,
          status: 'failed',
          attempt: 1,
          participants: [{ id: firstId, name: 'Northwind' }, { id: secondId, name: 'Verdant Accord' }],
          result: null,
          errorCode: 'engine_error',
        }), { status: 200 })
      }
      if (url.startsWith(`/v1/battles/${battleId}/events?`)) {
        return new Response(JSON.stringify({
          attempt: 1,
          events: [{
            attempt: 1,
            sequence: 1,
            tick: 3,
            type: 'battle_failed',
            data: { detail: null, context: { phase: 'advance' }, retryable: false, attempts: 3 },
            createdAt: '2026-08-21T00:00:00Z',
          }],
          nextCursor: 1,
        }), { status: 200 })
      }
      throw new Error(`Unexpected fetch: ${url}`)
    })

    renderApp(`/battles/${battleId}`)

    expect(await screen.findByRole('heading', { name: 'Battle failed' })).toBeInTheDocument()
    expect(screen.getByText('engine_error')).toBeInTheDocument()
    expect(await screen.findByText('{"phase":"advance"}')).toBeInTheDocument()
    expect(screen.getByText('—')).toBeInTheDocument()
    expect(screen.getByText('false')).toBeInTheDocument()
    expect(screen.getByText('3', { selector: 'dd' })).toBeInTheDocument()
  })
})
