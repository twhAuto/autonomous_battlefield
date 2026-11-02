// Exercises user flows across routes with mocked APIs, including authentication, faction
// management, battle creation, and live battle controls.

import { doctrineTemplates } from '../features/factions/pages/CreateFactionPage'
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import type { AuthenticationClient, AuthSession } from '../features/auth/api/auth'

const firstId = '7f22e693-bacd-46bb-b942-64fa613cd388'
const secondId = '4db8d152-1e04-4d10-8824-859c6ed72878'
const battleId = '41ac3a2f-3371-4de1-b0f6-70600fcd9052'
const ruleset = {
  id: 'castle-siege-v1', version: '1', designBudget: 120, startingGold: 60,
  units: [
    { id: 'vanguard', cost: 35, health: 70, attack: 9, defense: 7, speed: 4, movement: 1, range: 1, vision: 2 },
    { id: 'striker', cost: 25, health: 42, attack: 14, defense: 3, speed: 8, movement: 2, range: 1, vision: 3 },
    { id: 'ranger', cost: 30, health: 30, attack: 11, defense: 2, speed: 6, movement: 1, range: 3, vision: 4 },
  ],
  upgrades: [{ id: 'arsenal', cost: 20, attackBonus: 2, defenseBonus: 0, economyIncomeBonus: 0 }],
}

const authenticatedSession: AuthSession = { enabled: true, authenticated: true, actor: { id: 'test-admin' } }

function renderApp(path = '/', authentication?: AuthenticationClient) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App authentication={authentication} initialSession={authenticatedSession} />
    </MemoryRouter>,
  )
}

function eventPageResponse(firstSequence: number, lastSequence: number, tick: number) {
  return new Response(JSON.stringify({
    attempt: 1,
    events: Array.from({ length: lastSequence - firstSequence + 1 }, (_, index) => ({
      attempt: 1,
      sequence: firstSequence + index,
      tick,
      type: 'tick_completed',
      data: {},
      createdAt: '2026-08-21T00:00:00Z',
    })),
    nextCursor: lastSequence,
  }), { status: 200 })
}

describe('App', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
    vi.spyOn(crypto, 'randomUUID').mockReset()
      .mockReturnValueOnce(firstId)
      .mockReturnValue(secondId)
  })

  it('requires a successful login before rendering application routes', async () => {
    const authentication: AuthenticationClient = {
      session: vi.fn().mockResolvedValue({ enabled: true, authenticated: false }),
      login: vi.fn().mockResolvedValue(authenticatedSession),
      logout: vi.fn().mockResolvedValue(undefined),
    }
    const user = userEvent.setup()
    render(
      <MemoryRouter>
        <App authentication={authentication} />
      </MemoryRouter>,
    )

    expect(await screen.findByRole('heading', { name: 'Identify commander.' })).toBeInTheDocument()
    await user.type(screen.getByLabelText('Username'), 'admin')
    await user.type(screen.getByLabelText('Password'), 'test-password')
    await user.click(screen.getByRole('button', { name: /ENTER COMMAND POST/i }))

    expect(authentication.login).toHaveBeenCalledWith('admin', 'test-password')
    expect(await screen.findByRole('heading', { name: 'Main menu' })).toBeInTheDocument()
  })

  it('shows a generic login failure and can log out an authenticated session', async () => {
    const authentication: AuthenticationClient = {
      session: vi.fn().mockResolvedValue({ enabled: true, authenticated: false }),
      login: vi.fn().mockRejectedValue(new Error('The username or password is incorrect.')),
      logout: vi.fn().mockResolvedValue(undefined),
    }
    const user = userEvent.setup()
    const view = render(
      <MemoryRouter>
        <App authentication={authentication} />
      </MemoryRouter>,
    )

    await user.type(await screen.findByLabelText('Username'), 'admin')
    await user.type(screen.getByLabelText('Password'), 'wrong-password')
    await user.click(screen.getByRole('button', { name: /ENTER COMMAND POST/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent('The username or password is incorrect.')

    view.unmount()
    render(
      <MemoryRouter>
        <App authentication={authentication} initialSession={authenticatedSession} />
      </MemoryRouter>,
    )
    await user.click(screen.getByRole('button', { name: 'Log out test-admin' }))
    expect(authentication.logout).toHaveBeenCalled()
    expect(await screen.findByRole('heading', { name: 'Identify commander.' })).toBeInTheDocument()
  })

  it.each(['local-password', 'cloudflare'])('keeps the %s session visible when logout fails and allows retry', async (mode) => {
    const authentication: AuthenticationClient = {
      session: vi.fn(),
      login: vi.fn(),
      logout: vi.fn().mockRejectedValueOnce(new Error('Request failed')).mockResolvedValueOnce(undefined),
    }
    const user = userEvent.setup()
    render(<MemoryRouter><App authentication={authentication} initialSession={{
      enabled: true, authenticated: true, mode, actor: { id: 'test-admin', role: 'admin' },
    }} /></MemoryRouter>)

    await user.click(screen.getByRole('button', { name: 'Log out test-admin' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Logout could not be confirmed. Try Log out again.')
    expect(screen.getByRole('heading', { name: 'Main menu' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: /Identify commander|Sign in to Battlefield/ })).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Log out test-admin' }))
    expect(await screen.findByRole('heading', { name: mode === 'cloudflare' ? 'Sign in to Battlefield' : 'Identify commander.' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(authentication.logout).toHaveBeenCalledTimes(2)
  })

  it('shows a signing-out screen while the browser leaves for the Access logout page', async () => {
    const authentication: AuthenticationClient = { session: vi.fn(), login: vi.fn(), logout: vi.fn().mockResolvedValue(true) }
    const user = userEvent.setup()
    render(<MemoryRouter><App authentication={authentication} initialSession={{
      enabled: true, authenticated: true, mode: 'cloudflare', actor: { id: 'test-admin', role: 'admin' },
    }} /></MemoryRouter>)

    await user.click(screen.getByRole('button', { name: 'Log out test-admin' }))
    expect(await screen.findByRole('heading', { name: 'Signing out…' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Sign in to Battlefield' })).not.toBeInTheDocument()
    expect(screen.getAllByRole('button').map(button => button.textContent)).toEqual(['SIGN OUT AGAIN→'])
  })

  it('offers the Access logout again when the browser stayed, and returns to the app if it fails', async () => {
    const authentication: AuthenticationClient = { session: vi.fn(), login: vi.fn(), logout: vi.fn().mockResolvedValueOnce(true).mockResolvedValueOnce(true).mockRejectedValueOnce(new Error('offline')) }
    const user = userEvent.setup()
    render(<MemoryRouter><App authentication={authentication} initialSession={{
      enabled: true, authenticated: true, mode: 'cloudflare', actor: { id: 'test-admin', role: 'admin' },
    }} /></MemoryRouter>)

    await user.click(screen.getByRole('button', { name: 'Log out test-admin' }))
    await user.click(await screen.findByRole('button', { name: /sign out again/i }))
    expect(authentication.logout).toHaveBeenCalledTimes(2)
    expect(screen.getByRole('heading', { name: 'Signing out…' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /sign out again/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Logout could not be confirmed. Try Log out again.')
    expect(screen.getByRole('heading', { name: 'Main menu' })).toBeInTheDocument()
  })

  it.each(['local-password', 'cloudflare'])('signs out an expired %s session when logout returns 401', async (mode) => {
    vi.mocked(fetch).mockResolvedValueOnce(new Response(null, { status: 401 }))
    const user = userEvent.setup()
    render(<MemoryRouter><App initialSession={{
      enabled: true, authenticated: true, mode, actor: { id: 'test-admin', role: 'admin' },
    }} /></MemoryRouter>)

    await user.click(screen.getByRole('button', { name: 'Log out test-admin' }))
    expect(await screen.findByRole('heading', { name: mode === 'cloudflare' ? 'Sign in to Battlefield' : 'Identify commander.' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it.each(['admin', 'tester', 'player'] as const)('offers the %s main-menu destinations allowed by primary navigation', (role) => {
    render(<MemoryRouter><App initialSession={{
      enabled: true, authenticated: true, mode: 'cloudflare', actor: { id: 'test-user', role },
    }} /></MemoryRouter>)
    const menu = screen.getByRole('region', { name: 'Main menu' })

    expect(within(menu).getByRole('heading', { name: 'Factions' }).closest('a')).toHaveAttribute('href', '/factions')
    expect(within(menu).getByRole('link', { name: /stage battle/i })).toHaveAttribute('href', '/battles/new')
    expect(within(menu).getByRole('link', { name: /battle history/i })).toHaveAttribute('href', '/battles')
    expect(within(menu).getAllByRole('link')).toHaveLength(5)
    for (const [name, href] of [['Leagues', '/leagues'], ['Statistics', '/analytics']]) {
      expect(within(menu).getByRole('link', { name })).toHaveAttribute('href', href)
    }
  })

  it('restricts the admin route to administrators', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify({
      version: 1,
      policy: { providers: {}, profiles: {} },
      models: [],
    })))
    const playerSession: AuthSession = { enabled: true, authenticated: true, mode: 'cloudflare', actor: { id: 'player', role: 'player' } }
    const { unmount } = render(<MemoryRouter initialEntries={['/admin']}><App initialSession={playerSession} /></MemoryRouter>)
    expect(screen.getByRole('heading', { name: 'This route is outside the mapped frontier.' })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Admin' })).not.toBeInTheDocument()
    unmount()

    renderApp('/admin')
    expect(screen.getByRole('heading', { name: 'Control center' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Admin' })).toBeInTheDocument()
    expect(await screen.findByText('No AI model profiles are configured.')).toBeInTheDocument()
  })

  it('navigates from the main menu to faction creation', async () => {
    const user = userEvent.setup()
    renderApp()
	const menu = screen.getByRole('heading', { name: 'Main menu' }).closest('section')!

    await user.click(within(menu).getByRole('heading', { name: 'Factions' }).closest('a')!)
    await user.click(screen.getByRole('link', { name: '+ CREATE FACTION' }))

    expect(screen.getByRole('heading', { name: 'Create a faction' })).toBeInTheDocument()
    expect(screen.getByLabelText('Faction name')).toBeInTheDocument()
  })

  it('creates a faction from its dedicated route', async () => {
	vi.mocked(fetch).mockImplementation(async (input) => String(input) === '/v1/scenarios' ? new Response(JSON.stringify({ scenarios: [] }), { status: 200 }) : String(input) === '/v1/faction-rulesets'
	  ? new Response(JSON.stringify({ rulesets: [ruleset] }), { status: 200 })
	  : new Response(null, { status: 201 }))
    const user = userEvent.setup()
    renderApp('/factions/new')

	await user.type(await screen.findByLabelText('Faction name'), '  The Verdant Accord  ')
	await user.click(screen.getByRole('button', { name: /CREATE FACTION/i }))

    expect(fetch).toHaveBeenCalledWith('/v1/factions', {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' },
	  body: JSON.stringify({
	    id: firstId, name: 'The Verdant Accord', doctrine: doctrineTemplates.Balanced, background: '', emblem: 'star',
	  }),
    })
    expect(await screen.findByText('The Verdant Accord has entered the battlefield.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /view faction registry/i })).toHaveAttribute('href', '/factions')
  })

  it('shows the faction blueprint validation detail returned by the server', async () => {
	vi.mocked(fetch).mockImplementation(async (input) => String(input) === '/v1/scenarios' ? new Response(JSON.stringify({ scenarios: [] }), { status: 200 }) : String(input) === '/v1/faction-rulesets'
	  ? new Response(JSON.stringify({ rulesets: [ruleset] }), { status: 200 })
	  : new Response('invalid faction blueprint: recruitment priority is incomplete\n', { status: 422 }))
    const user = userEvent.setup()
    renderApp('/factions/new')

	await user.type(await screen.findByLabelText('Faction name'), 'Northwind')
	await user.click(screen.getByRole('button', { name: /CREATE FACTION/i }))

	expect(await screen.findByText('invalid faction blueprint: recruitment priority is incomplete')).toBeInTheDocument()
  })

  it('keeps faction identifiers out of the creation UI', async () => {
	vi.mocked(fetch).mockImplementation(async () => new Response(JSON.stringify({ rulesets: [ruleset], scenarios: [] }), { status: 200 }))
    renderApp('/factions/new')

    expect(screen.queryByRole('button', { name: 'Generate a new faction identifier' })).not.toBeInTheDocument()
    expect(screen.queryByText(firstId)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /copy.*identifier/i })).not.toBeInTheDocument()
  })

  it('uses editable doctrine templates', async () => {
    const user = userEvent.setup()
    renderApp('/factions/new')
    await user.click(screen.getByRole('button', { name: 'Aggressive' }))
    expect(screen.getByLabelText('Doctrine')).toHaveValue(doctrineTemplates.Aggressive)
    await user.clear(screen.getByLabelText('Doctrine'))
    expect(screen.getByRole('button', {name: 'CREATE FACTION'})).toBeDisabled()
  })

  it('renders every faction returned by the registry', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify({ factions: [
      { id: firstId, name: 'Northwind' },
      { id: secondId, name: 'Verdant Accord' },
    ] }), { status: 200, headers: { 'content-type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' } }))

    renderApp('/factions')

    expect(await screen.findByRole('heading', { name: 'Northwind' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Verdant Accord' })).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/v1/factions', expect.objectContaining({ signal: expect.any(AbortSignal) }))
  })

  it('deletes a faction from the registry after confirmation', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    let deleted = false
    vi.mocked(fetch).mockImplementation(async (input, init) => {
      if (String(input) === '/v1/factions' && !init?.method) {
        return new Response(JSON.stringify({ factions: deleted ? [] : [{ id: firstId, name: 'Northwind', doctrine: 'Defend' }] }), { status: 200 })
      }
      if (String(input) === `/v1/factions/${firstId}` && init?.method === 'DELETE') {
        deleted = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`Unexpected fetch: ${String(input)}`)
    })
    const user = userEvent.setup()
    renderApp(`/factions/${firstId}/edit`)
    await user.click(await screen.findByRole('button', { name: 'DELETE Northwind' }))

    expect(confirm).toHaveBeenCalledWith('Delete Northwind? This removes it from the active faction registry.')
    expect(fetch).toHaveBeenCalledWith(`/v1/factions/${firstId}`, { method: 'DELETE', headers: { 'X-Requested-With': 'XMLHttpRequest' } })
    expect(await screen.findByRole('heading', { name: 'The frontier is unclaimed' })).toBeInTheDocument()
    confirm.mockRestore()
  })

  it('shows an empty registry with a create action', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify({ factions: [] }), {
      status: 200,
      headers: { 'content-type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' },
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
          { id: firstId, name: 'Northwind', rulesetId: 'castle-siege-v1', blueprint: { units: [{ type: 'vanguard', count: 1 }], upgradeIds: null, recruitmentPriority: ['vanguard'] } },
          { id: secondId, name: 'Verdant Accord', rulesetId: 'castle-siege-v1', blueprint: { units: [{ type: 'vanguard', count: 1 }], upgradeIds: null, recruitmentPriority: ['vanguard'] } },
        ] }), { status: 200 })
      }
      if (url === '/v1/scenarios') {
        return new Response(JSON.stringify({ scenarios: [{
          id: 'skirmish-v1', rulesetId: 'castle-siege-v1', allowedUnits: ['vanguard'], allowedUpgrades: [], rulesVersion: '1', maximumTicks: 500, mapWidth: 15, mapHeight: 11, tickDuration: '0s', decisionIntervalTicks: 10,
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
          state: {
            battleId, tick: 0, openingComplete: false,
            factions: [firstId, secondId].map((factionId) => ({ factionId, gold: 60, goldEarned: 0, goldSpent: 0, goldLooted: 0 })),
            units: null, buildings: null,
          },
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
    expect(screen.getByLabelText('Maximum ticks')).toHaveValue(25)
    expect(screen.getByLabelText('Slot one')).toHaveValue(firstId)
    expect(screen.getByLabelText('Slot two')).toHaveValue(secondId)
    const deployButton = screen.getByRole('button', { name: 'DEPLOY COMBATANTS' })
    expect(deployButton).toBeEnabled()
    await user.click(deployButton)

    expect(fetch).toHaveBeenCalledWith('/v1/battles', {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' },
      body: JSON.stringify({
        id: battleId,
        scenarioId: 'skirmish-v1',
        participants: [
          { factionId: firstId, controller: { type: 'scripted' } },
          { factionId: secondId, controller: { type: 'scripted' } },
        ],
        tickCount: 25,
      }),
    })
    expect(await screen.findByRole('heading', { name: 'Battle in progress' })).toBeInTheDocument()
    expect(screen.getByText('Awaiting engine')).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Inspect' })).toHaveValue('system')
    await user.selectOptions(screen.getByRole('combobox', { name: 'Inspect' }), firstId)
    const inspector = within(screen.getByRole('complementary', { name: 'Battle inspector' }))
    expect(inspector.getByRole('tab', { name: 'Overview' })).toHaveAttribute('aria-selected', 'true')
    await user.click(screen.getByRole('tab', { name: 'Decisions' }))
    expect(screen.getByRole('region', { name: 'AI decisions' })).toHaveTextContent('No decisions recorded for this player in the current attempt.')
  })

  it('shows battle tick validation before deployment', async () => {
    vi.mocked(fetch).mockImplementation(async (input) => {
      const url = String(input)
      if (url === '/v1/factions') {
        return new Response(JSON.stringify({ factions: [
          { id: firstId, name: 'Northwind', rulesetId: 'castle-siege-v1' },
          { id: secondId, name: 'Verdant Accord', rulesetId: 'castle-siege-v1' },
        ] }), { status: 200 })
      }
      if (url === '/v1/scenarios') {
        return new Response(JSON.stringify({ scenarios: [{
          id: 'skirmish-v1', rulesetId: 'castle-siege-v1', rulesVersion: '1', maximumTicks: 500, mapWidth: 15, mapHeight: 11, tickDuration: '0s', decisionIntervalTicks: 10,
        }] }), { status: 200 })
      }
      throw new Error(`Unexpected fetch: ${url}`)
    })
    const user = userEvent.setup()
    renderApp('/battles/new')

    const ticks = await screen.findByLabelText('Maximum ticks')
    await user.clear(ticks)
    await user.type(ticks, '501')

    expect(screen.getByText('Enter a whole number from 1 to 500.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'DEPLOY COMBATANTS' })).toBeDisabled()
  })

  it('submits an available model profile as a model controller', async () => {
    vi.mocked(crypto.randomUUID).mockReset().mockReturnValue(battleId)
    vi.mocked(fetch).mockImplementation(async (input, init) => {
      const url = String(input)
      if (url === '/v1/factions') {
        return new Response(JSON.stringify({ factions: [
          { id: firstId, name: 'Northwind', rulesetId: 'castle-siege-v1' },
          { id: secondId, name: 'Verdant Accord', rulesetId: 'castle-siege-v1' },
        ] }), { status: 200 })
      }
      if (url === '/v1/scenarios') {
        return new Response(JSON.stringify({ scenarios: [{
          id: 'castle-siege-v1', rulesetId: 'castle-siege-v1', rulesVersion: '1', maximumTicks: 500,
          mapWidth: 15, mapHeight: 11, tickDuration: '1s', decisionIntervalTicks: 10,
        }] }), { status: 200 })
      }
      if (url === '/v1/model-profiles') {
        return new Response(JSON.stringify({ profiles: [
          { id: 'gemini-api-balanced', displayName: 'Gemini API', provider: 'gemini', runtime: 'api', scope: 'cluster', modelLabel: 'gemini-2.5-flash', available: true, toolCapability: true },
          { id: 'codex-local', displayName: 'Codex local', provider: 'codex-cli', runtime: 'cli', scope: 'local', modelLabel: '', available: false, toolCapability: true },
        ] }), { status: 200 })
      }
      if (url === '/v1/battles/allowance') {
        return new Response(JSON.stringify({ limited: false, startedToday: 0, unfinished: 0, serviceUnfinished: 0, resetAt: '2099-01-01T00:00:00Z' }), { status: 200 })
      }
      if (url === '/v1/battles' && init?.method === 'POST') {
        return new Response('provider unavailable', { status: 503 })
      }
      throw new Error(`Unexpected fetch: ${url}`)
    })
    const user = userEvent.setup()
    renderApp('/battles/new')

    const controllers = await screen.findAllByLabelText('Controller')
    await user.selectOptions(controllers[0], 'gemini-api-balanced')
    expect(screen.queryByRole('option', { name: /Codex local/i })).not.toBeInTheDocument()
    expect(screen.queryByText('AI integration status')).not.toBeInTheDocument()
    // Factions, scenarios, profiles and the battle allowance; choosing a model reads nothing more.
    expect(fetch).toHaveBeenCalledTimes(4)
    expect(screen.queryByRole('button', { name: 'Check availability' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'DEPLOY COMBATANTS' }))

    expect(fetch).toHaveBeenCalledWith('/v1/battles', expect.objectContaining({
      method: 'POST',
      body: expect.stringContaining('"profileId":"gemini-api-balanced"'),
    }))
    expect(await screen.findByText('The battle could not be queued. Please try again.')).toBeInTheDocument()
  })

  it('renders a completed battle result and its event stream', async () => {
    const user = userEvent.setup()
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
          participants: [
            { id: firstId, name: 'Northwind', controller: { type: 'model', profileId: 'gemini-api-balanced', modelIdentifier: 'gemini-2.5-flash' } },
            { id: secondId, name: 'Verdant Accord', controller: { type: 'scripted' } },
          ],
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
      if (url === `/v1/battles/${battleId}/decisions?attempt=1`) {
        return new Response(JSON.stringify({ decisions: [{
          requestId: 'decision-1', factionId: firstId, targetTick: 10, status: 'accepted', controllerType: 'model',
          profileId: 'gemini-api-balanced', provider: 'gemini', runtime: 'api',
          modelIdentifier: 'gemini-2.5-flash', resolvedPlan: {
            name: 'pressure', rationale: 'The visible lane is open, so ranger pressure can cover the advance.', actionWeights: { attack: 1400, defend: 800 }, reserveGold: 30,
            spendOrder: ['economy', 'units'], recruitmentPriorities: ['ranger', 'vanguard'],
            constructionPriorities: [{ x: 2, y: 2 }],
          },
          usage: { inputTokens: 100, outputTokens: 20, requests: 2 }, createdAt: '2026-08-21T00:00:00Z',
          resolvedAt: '2026-08-21T00:00:01Z',
        }] }), { status: 200 })
      }
      if (url.startsWith(`/v1/battles/${battleId}/events?`)) {
        return new Response(JSON.stringify({
          attempt: 1,
          events: [
            { attempt: 1, sequence: 1, tick: 1, type: 'battle_started', data: { scenarioId: 'skirmish-v1' }, createdAt: '2026-08-21T00:00:00Z' },
            { attempt: 1, sequence: 2, tick: 18, type: 'battle_completed', data: { reason: 'castle_destroyed' }, createdAt: '2026-08-21T00:00:01Z' },
          ],
          nextCursor: 2,
        }), { status: 200 })
      }
      throw new Error(`Unexpected fetch: ${url}`)
    })

    renderApp(`/battles/${battleId}`)

    expect(await screen.findByRole('heading', { name: 'Battle complete' })).toBeInTheDocument()
    expect(screen.getByText('Northwind wins')).toBeInTheDocument()
    expect(await screen.findByText('battle started', { selector: 'strong' })).toBeInTheDocument()
    expect((await screen.findAllByRole('listitem'))[0]).toHaveTextContent('battle completed')
    expect(screen.getByRole('heading', { name: 'Latest events' })).toBeInTheDocument()
    expect(screen.getAllByText('Damage dealt')).toHaveLength(2)
    await user.selectOptions(screen.getByRole('combobox', { name: 'Inspect' }), firstId)
    await user.click(screen.getByRole('tab', { name: 'Decisions' }))
    expect(await screen.findByRole('region', { name: 'AI decisions' })).toHaveTextContent('pressure')
    expect(screen.getByRole('region', { name: 'AI decisions' })).toHaveTextContent('attack 1400 · defend 800')
    expect(screen.getByRole('region', { name: 'AI decisions' })).toHaveTextContent('The visible lane is open')
    expect(screen.getByRole('region', { name: 'AI decisions' })).toHaveTextContent('Ranger → Vanguard')
    expect(within(screen.getByRole('region', { name: 'AI decisions' })).getByText('gemini-2.5-flash')).toBeInTheDocument()
  })

  it('stops an active battle from the battle control panel', async () => {
    vi.mocked(fetch).mockImplementation(async (input, init) => {
      const url = String(input)
      if (url === `/v1/battles/${battleId}/stop` && init?.method === 'POST') {
        return new Response(JSON.stringify({
          id: battleId, scenarioId: 'castle-siege-v1', scenarioDigest: 'digest', requestedTicks: 100,
          currentTick: 9, status: 'stopped', attempt: 1,
          participants: [{ id: firstId, name: 'Northwind', controller: { type: 'model', profileId: 'codex-subscription-local' } }, { id: secondId, name: 'Verdant', controller: { type: 'scripted' } }],
          result: null,
        }), { status: 200 })
      }
      if (url === `/v1/battles/${battleId}`) {
        return new Response(JSON.stringify({
          id: battleId, scenarioId: 'castle-siege-v1', scenarioDigest: 'digest', requestedTicks: 100,
          currentTick: 9, status: 'running', attempt: 1,
          participants: [{ id: firstId, name: 'Northwind', controller: { type: 'model', profileId: 'codex-subscription-local' } }, { id: secondId, name: 'Verdant', controller: { type: 'scripted' } }],
          result: null,
        }), { status: 200 })
      }
      if (url === `/v1/battles/${battleId}/decisions?attempt=1`) {
        return new Response(JSON.stringify({ decisions: [] }), { status: 200 })
      }
      if (url.startsWith(`/v1/battles/${battleId}/events?`)) {
        return new Response(JSON.stringify({ attempt: 1, events: [], nextCursor: null }), { status: 200 })
      }
      throw new Error(`Unexpected fetch: ${url}`)
    })
    const user = userEvent.setup()
    renderApp(`/battles/${battleId}`)

    await user.click(await screen.findByRole('button', { name: 'STOP' }))

    expect(fetch).toHaveBeenCalledWith(`/v1/battles/${battleId}/stop`, { method: 'POST', headers: { 'X-Requested-With': 'XMLHttpRequest' } })
    expect(await screen.findByRole('heading', { name: 'Battle stopped' })).toBeInTheDocument()
    expect(screen.getByText('Stopped by operator')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'STOP' })).not.toBeInTheDocument()
  })

  it('pauses and resumes an active battle from the battle control panel', async () => {
    let paused = false
    vi.mocked(fetch).mockImplementation(async (input, init) => {
      const url = String(input)
      if (url === `/v1/battles/${battleId}/pause` && init?.method === 'POST') paused = true
      else if (url === `/v1/battles/${battleId}/resume` && init?.method === 'POST') paused = false
      else if (url === `/v1/battles/${battleId}/decisions?attempt=1`) return new Response(JSON.stringify({ decisions: [] }), { status: 200 })
      else if (url.startsWith(`/v1/battles/${battleId}/events?`)) return new Response(JSON.stringify({ attempt: 1, events: [], nextCursor: null }), { status: 200 })
      else if (url !== `/v1/battles/${battleId}`) throw new Error(`Unexpected fetch: ${url}`)
      return new Response(JSON.stringify({
        id: battleId, scenarioId: 'castle-siege-v1', scenarioDigest: 'digest', requestedTicks: 100,
        currentTick: 12, status: 'running', paused, attempt: 1,
        participants: [{ id: firstId, name: 'Northwind' }, { id: secondId, name: 'Verdant' }], result: null,
      }), { status: 200 })
    })
    const user = userEvent.setup()
    renderApp(`/battles/${battleId}`)

    await user.click(await screen.findByRole('button', { name: 'PAUSE' }))
    expect(await screen.findByRole('heading', { name: 'Battle paused' })).toBeInTheDocument()
    expect(screen.getByText('Battle paused', { selector: 'strong' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'PLAY' }))
    expect(await screen.findByRole('heading', { name: 'Battle in progress' })).toBeInTheDocument()
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

  it('starts the event stream from the newest page', async () => {
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
      if (url.includes('after=0') && url.includes('latest=true')) {
        return new Response(JSON.stringify({
          attempt: 1,
          events: [
            { attempt: 1, sequence: 50, tick: 9, type: 'tick_completed', data: {}, createdAt: '2026-08-21T00:00:00Z' },
            { attempt: 1, sequence: 51, tick: 10, type: 'battle_completed', data: { reason: 'tick_limit' }, createdAt: '2026-08-21T00:00:01Z' },
          ],
          nextCursor: 51,
        }), { status: 200 })
      }
      throw new Error(`Unexpected fetch: ${url}`)
    })
    renderApp(`/battles/${battleId}`)

    expect(await screen.findByText('#0051')).toBeInTheDocument()
    expect(screen.getByText(/LATEST TICK 10/)).toBeInTheDocument()
    expect(vi.mocked(fetch).mock.calls.some(([input]) => String(input).includes('attempt=1&after=0&limit=50&latest=true'))).toBe(true)
  })

  it('automatically drains full event pages while a battle is running', async () => {
    vi.mocked(fetch).mockImplementation(async (input) => {
      const url = String(input)
      if (url === `/v1/battles/${battleId}`) {
        return new Response(JSON.stringify({
          id: battleId,
          scenarioId: 'castle-siege-v1',
          scenarioDigest: 'digest',
          requestedTicks: 500,
          currentTick: 38,
          status: 'running',
          attempt: 1,
          participants: [{ id: firstId, name: 'Northwind' }, { id: secondId, name: 'Verdant Accord' }],
          result: null,
        }), { status: 200 })
      }
      if (url.includes('after=0') && url.includes('latest=true')) {
        return eventPageResponse(1, 50, 36)
      }
      if (url.includes('after=50')) {
        return eventPageResponse(51, 100, 37)
      }
      if (url.includes('after=100')) {
        return eventPageResponse(101, 101, 38)
      }
      throw new Error(`Unexpected fetch: ${url}`)
    })

    vi.useFakeTimers()
    const view = renderApp(`/battles/${battleId}`)
    try {
      // Flush the initial status/latest-page requests before advancing polling.
      await act(async () => {})
      fireEvent.change(screen.getByLabelText('Event group'), { target: { value: 'all' } })
      expect(screen.getByText('#0050')).toBeInTheDocument()
      await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
      expect(screen.getByText('#0100')).toBeInTheDocument()
      expect(screen.queryByText('#0101')).not.toBeInTheDocument()
      // A full page must trigger an immediate drain, not another polling interval.
      await act(async () => { await vi.advanceTimersByTimeAsync(1) })
      expect(screen.getByText('#0101')).toBeInTheDocument()
      expect(screen.getByText(/LATEST TICK 38/)).toBeInTheDocument()
      expect(vi.mocked(fetch).mock.calls.some(([input]) => String(input).includes('after=100'))).toBe(true)
    } finally {
      view.unmount()
      vi.useRealTimers()
    }
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
    fireEvent.change(screen.getByLabelText('Event group'), { target: { value: 'all' } })
    expect(await screen.findByText('{"phase":"advance"}')).toBeInTheDocument()
    expect(within(screen.getByRole('listitem')).getByText('—')).toBeInTheDocument()
    expect(screen.getByText('false')).toBeInTheDocument()
    expect(screen.getByText('3', { selector: 'dd' })).toBeInTheDocument()
  })
})

describe('battle history navigation', () => {
  beforeEach(() => { vi.stubGlobal('fetch', vi.fn()) })
  it('navigates from history to staging with exact active links', async () => {
    const user = userEvent.setup()
    vi.mocked(fetch).mockImplementation(async (input) => {
      const path = String(input)
      if (path.startsWith('/v1/battles?')) return new Response(JSON.stringify({ battles: [], nextCursor: null }))
      if (path === '/v1/factions') return new Response(JSON.stringify({ factions: [] }))
      if (path === '/v1/scenarios') return new Response(JSON.stringify({ scenarios: [] }))
      return new Response(JSON.stringify({ profiles: [] }))
    })
    renderApp('/battles')
    await screen.findByText('No battles yet')
    const navigation = screen.getByRole('navigation', { name: 'Primary navigation' })
    expect(within(navigation).getByRole('link', { name: 'Battle history' })).toHaveAttribute('aria-current', 'page')
    await user.click(within(navigation).getByRole('link', { name: 'Stage battle' }))
    expect(within(navigation).getByRole('link', { name: 'Battle history' })).not.toHaveAttribute('aria-current')
    expect(within(navigation).getByRole('link', { name: 'Stage battle' })).toHaveAttribute('aria-current', 'page')
  })
  it('returns to login when history requests are unauthorized', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 401 }))
    renderApp('/battles')
    expect(await screen.findByRole('button', { name: /enter command post/i })).toBeInTheDocument()
  })
})

it('uses Access reauthentication without offering the local password form', async () => {
 const authentication: AuthenticationClient={session:vi.fn(),login:vi.fn(),logout:vi.fn().mockResolvedValue(undefined)}
 render(<MemoryRouter><App authentication={authentication} initialSession={{enabled:true,authenticated:false,mode:'cloudflare'}}/></MemoryRouter>)
 expect(screen.getByRole('heading',{name:'Sign in to Battlefield'})).toBeInTheDocument()
 expect(screen.queryByLabelText(/password/i)).not.toBeInTheDocument()
 expect(authentication.login).not.toHaveBeenCalled()
})

it('prevents a Player from opening another person’s faction editor directly',async()=>{
 vi.stubGlobal('fetch',vi.fn().mockResolvedValue(new Response(JSON.stringify({factions:[{id:firstId,name:'Shared',doctrine:'Defend',ownerId:'someone-else'}]}))))
 render(<MemoryRouter initialEntries={[`/factions/${firstId}/edit`]}><App initialSession={{enabled:true,authenticated:true,mode:'cloudflare',actor:{id:'friend',role:'player'}}}/></MemoryRouter>)
 expect(await screen.findByText('You can only edit your own factions.')).toBeInTheDocument()
 expect(screen.queryByRole('button',{name:'SAVE FACTION'})).not.toBeInTheDocument()
})
