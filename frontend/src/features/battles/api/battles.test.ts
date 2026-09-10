// Checks battle API request encoding, response decoding, control actions, perspectives,
// pagination, and error mapping.

import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  BattleApiError,
  createBattle,
  getBattle,
  getBattleView,
  listBattleEvents,
  listBattles,
  listDecisionHistory,
  listModelProfiles,
  listScenarios,
  pauseBattle,
  resumeBattle,
  stopBattle,
  type BattleRun,
  type CreateBattleInput,
} from './battles'

const battleId = '41ac3a2f-3371-4de1-b0f6-70600fcd9052'
const input: CreateBattleInput = {
  id: battleId,
  scenarioId: 'skirmish-v1',
  participants: [
    { factionId: '7f22e693-bacd-46bb-b942-64fa613cd388', controller: { type: 'scripted' } },
    { factionId: '4db8d152-1e04-4d10-8824-859c6ed72878', controller: { type: 'scripted' } },
  ],
  tickCount: 100,
}

describe('battle API', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })

  it('lists scenarios', async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse({ scenarios: [{ id: 'skirmish-v1' }] }))

    await expect(listScenarios()).resolves.toEqual([{ id: 'skirmish-v1' }])
    expect(fetch).toHaveBeenCalledWith('/v1/scenarios', { signal: undefined, headers: { 'X-Requested-With': 'XMLHttpRequest' } })
  })

  it('lists safe model profiles and maps failures', async () => {
    const profiles = [{ id: 'gemini-api-balanced', displayName: 'Gemini API', available: true }]
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse({ profiles }))
    await expect(listModelProfiles()).resolves.toEqual(profiles)

    vi.mocked(fetch).mockResolvedValueOnce(new Response(null, { status: 503 }))
    await expect(listModelProfiles()).rejects.toEqual(new BattleApiError('Model profiles could not be loaded.', 503))
  })

  it('maps scenario HTTP and network failures', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(new Response(null, { status: 503 }))
    await expect(listScenarios()).rejects.toMatchObject({
      name: 'BattleApiError',
      status: 503,
      message: 'Battle scenarios could not be loaded.',
    })

    vi.mocked(fetch).mockRejectedValueOnce(new TypeError('offline'))
    await expect(listScenarios()).rejects.toThrow('The command post is unreachable')

    const abort = new DOMException('aborted', 'AbortError')
    vi.mocked(fetch).mockRejectedValueOnce(abort)
    await expect(listScenarios()).rejects.toBe(abort)
  })

  it('creates a battle', async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse({ id: battleId, status: 'queued' }, 202))

    await expect(createBattle(input)).resolves.toEqual({ id: battleId, status: 'queued' })
    expect(fetch).toHaveBeenCalledWith('/v1/battles', {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' },
      body: JSON.stringify(input),
    })
  })

  it.each([
    [409, 'That battle identifier already exists'],
    [404, 'A selected faction or scenario no longer exists'],
    [400, 'The battle configuration was rejected'],
    [422, 'Both factions must use the scenario ruleset'],
    [429, 'Battle allowance reached. Queued, running, and paused battles count toward active limits.'],
    [500, 'The battle could not be queued'],
  ])('maps create status %d', async (status, message) => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status }))
    await expect(createBattle(input)).rejects.toMatchObject({ status, message: expect.stringContaining(message) })
  })

  it('gets a battle and URL-encodes its ID', async () => {
    const run = { id: 'battle/id', status: 'running' } as BattleRun
    vi.mocked(fetch).mockResolvedValue(jsonResponse(run))

    await expect(getBattle('battle/id')).resolves.toEqual(run)
    expect(fetch).toHaveBeenCalledWith('/v1/battles/battle%2Fid', { signal: undefined, headers: { 'X-Requested-With': 'XMLHttpRequest' } })
  })

  it.each([
    [404, 'This battle could not be found.'],
    [503, 'Battle status could not be loaded.'],
  ])('maps get status %d', async (status, message) => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status }))
    await expect(getBattle(battleId)).rejects.toEqual(new BattleApiError(message, status))
  })

  it('stops a battle and maps stop failures', async () => {
    const stopped = { id: battleId, status: 'stopped' } as BattleRun
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse(stopped))
    await expect(stopBattle('battle/id')).resolves.toEqual(stopped)
    expect(fetch).toHaveBeenLastCalledWith('/v1/battles/battle%2Fid/stop', { method: 'POST', headers: { 'X-Requested-With': 'XMLHttpRequest' } })

    for (const [status, message] of [
      [400, 'identifier is invalid'],
      [404, 'could not be found'],
      [409, 'already ended'],
      [503, 'could not be stopped'],
    ] as const) {
      vi.mocked(fetch).mockResolvedValueOnce(new Response(null, { status }))
      await expect(stopBattle(battleId)).rejects.toMatchObject({ status, message: expect.stringContaining(message) })
    }
  })

  it('pauses and resumes a battle', async () => {
    const paused = { id: battleId, status: 'running', paused: true } as BattleRun
    const resumed = { ...paused, paused: false }
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse(paused)).mockResolvedValueOnce(jsonResponse(resumed))

    await expect(pauseBattle('battle/id')).resolves.toEqual(paused)
    expect(fetch).toHaveBeenLastCalledWith('/v1/battles/battle%2Fid/pause', { method: 'POST', headers: { 'X-Requested-With': 'XMLHttpRequest' } })
    await expect(resumeBattle('battle/id')).resolves.toEqual(resumed)
    expect(fetch).toHaveBeenLastCalledWith('/v1/battles/battle%2Fid/resume', { method: 'POST', headers: { 'X-Requested-With': 'XMLHttpRequest' } })
  })

  it('gets faction-filtered battle views', async () => {
    const view = { map: { width: 15 }, state: { tick: 3 }, visiblePositions: [] }
    vi.mocked(fetch).mockResolvedValue(jsonResponse(view))
    await expect(getBattleView('battle/id', 'faction/id')).resolves.toEqual(view)
    expect(fetch).toHaveBeenCalledWith('/v1/battles/battle%2Fid/factions/faction%2Fid/view', { signal: undefined, headers: { 'X-Requested-With': 'XMLHttpRequest' } })
  })

  it.each([
    [400, 'That faction perspective is invalid.'],
    [404, 'This battle could not be found.'],
    [503, 'Battle perspective could not be loaded.'],
  ])('maps perspective status %d', async (status, message) => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status }))
    await expect(getBattleView(battleId, 'faction')).rejects.toEqual(new BattleApiError(message, status))
  })

  it('lists events with default pagination', async () => {
    const page = { attempt: 2, events: [], nextCursor: null }
    vi.mocked(fetch).mockImplementation(async () => jsonResponse(page))

    await expect(listBattleEvents('battle/id', { attempt: 2 })).resolves.toEqual(page)
    expect(fetch).toHaveBeenCalledWith(
      '/v1/battles/battle%2Fid/events?attempt=2&after=0&limit=50',
      { signal: undefined, headers: { 'X-Requested-With': 'XMLHttpRequest' } },
    )
  })

	it('requests the newest event page', async () => {
		const page = { attempt: 2, events: [], nextCursor: null }
		vi.mocked(fetch).mockImplementation(async () => jsonResponse(page))

		await expect(listBattleEvents('battle/id', { attempt: 2, latest: true })).resolves.toEqual(page)
		expect(fetch).toHaveBeenCalledWith(
			'/v1/battles/battle%2Fid/events?attempt=2&after=0&limit=50&latest=true',
			{ signal: undefined, headers: { 'X-Requested-With': 'XMLHttpRequest' } },
		)
	})

  it.each([
    [404, 'This battle could not be found.'],
    [400, 'That battle attempt or event cursor is invalid.'],
    [503, 'Battle events could not be loaded.'],
  ])('maps event status %d', async (status, message) => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status }))
    await expect(listBattleEvents(battleId, { attempt: 1, after: 4, limit: 25 }))
      .rejects.toEqual(new BattleApiError(message, status))
  })

  it('lists decision history only for active attempts', async () => {
    await expect(listDecisionHistory(battleId, 0)).resolves.toEqual([])
    expect(fetch).not.toHaveBeenCalled()

    const decisions = [{ requestId: 'request-1', factionId: input.participants[0].factionId, targetTick: 10, status: 'accepted', usage: {} }]
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse({ decisions }))
    await expect(listDecisionHistory('battle/id', 2)).resolves.toEqual(decisions)
    expect(fetch).toHaveBeenLastCalledWith('/v1/battles/battle%2Fid/decisions?attempt=2', { signal: undefined, headers: { 'X-Requested-With': 'XMLHttpRequest' } })

    vi.mocked(fetch).mockResolvedValueOnce(new Response(null, { status: 503 }))
    await expect(listDecisionHistory(battleId, 1)).rejects.toEqual(new BattleApiError('Decision history could not be loaded.', 503))
  })
})

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'content-type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' },
  })
}

describe('battle history API', () => {
  beforeEach(() => { vi.stubGlobal('fetch', vi.fn()) })
  it('encodes queries, preserves cancellation, and returns the page', async () => {
    const page = { battles: [], nextCursor: null }
    vi.mocked(fetch).mockImplementation(async () => jsonResponse(page))
    const signal = new AbortController().signal
    await expect(listBattles({ status: 'paused', cursor: 'a+b', limit: 10 }, signal)).resolves.toEqual(page)
    expect(fetch).toHaveBeenLastCalledWith('/v1/battles?status=paused&cursor=a%2Bb&limit=10', { signal, headers: { 'X-Requested-With': 'XMLHttpRequest' } })
    await listBattles({})
    expect(fetch).toHaveBeenLastCalledWith('/v1/battles?limit=25', { signal: undefined, headers: { 'X-Requested-With': 'XMLHttpRequest' } })
  })
  it.each([400, 500])('maps HTTP %s', async (status) => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status }))
    await expect(listBattles({})).rejects.toMatchObject({ status, message: status === 400 ? 'That battle history query is invalid.' : 'Battle history could not be loaded.' })
  })
  it('notifies the app when authentication expires', async () => {
    const listener = vi.fn()
    window.addEventListener('battlefield:authentication-required', listener)
    try {
      vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 401 }))
      await expect(listBattles({})).rejects.toMatchObject({ status: 401 })
      expect(listener).toHaveBeenCalledOnce()
    } finally {
      window.removeEventListener('battlefield:authentication-required', listener)
    }
  })
})
