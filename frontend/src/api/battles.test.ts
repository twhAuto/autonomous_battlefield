import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  BattleApiError,
  createBattle,
  getBattle,
  listBattleEvents,
  listScenarios,
  type BattleRun,
  type CreateBattleInput,
} from './battles'

const battleId = '41ac3a2f-3371-4de1-b0f6-70600fcd9052'
const input: CreateBattleInput = {
  id: battleId,
  scenarioId: 'skirmish-v1',
  factionIds: ['7f22e693-bacd-46bb-b942-64fa613cd388', '4db8d152-1e04-4d10-8824-859c6ed72878'],
  tickCount: 100,
}

describe('battle API', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })

  it('lists scenarios', async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse({ scenarios: [{ id: 'skirmish-v1' }] }))

    await expect(listScenarios()).resolves.toEqual([{ id: 'skirmish-v1' }])
    expect(fetch).toHaveBeenCalledWith('/v1/scenarios', { signal: undefined })
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
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    })
  })

  it.each([
    [409, 'That battle identifier already exists'],
    [404, 'A selected faction or scenario no longer exists'],
    [400, 'The battle configuration was rejected'],
    [500, 'The battle could not be queued'],
  ])('maps create status %d', async (status, message) => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status }))
    await expect(createBattle(input)).rejects.toMatchObject({ status, message: expect.stringContaining(message) })
  })

  it('gets a battle and URL-encodes its ID', async () => {
    const run = { id: 'battle/id', status: 'running' } as BattleRun
    vi.mocked(fetch).mockResolvedValue(jsonResponse(run))

    await expect(getBattle('battle/id')).resolves.toEqual(run)
    expect(fetch).toHaveBeenCalledWith('/v1/battles/battle%2Fid', { signal: undefined })
  })

  it.each([
    [404, 'This battle could not be found.'],
    [503, 'Battle status could not be loaded.'],
  ])('maps get status %d', async (status, message) => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status }))
    await expect(getBattle(battleId)).rejects.toEqual(new BattleApiError(message, status))
  })

  it('lists events with default pagination', async () => {
    const page = { attempt: 2, events: [], nextCursor: null }
    vi.mocked(fetch).mockResolvedValue(jsonResponse(page))

    await expect(listBattleEvents('battle/id', { attempt: 2 })).resolves.toEqual(page)
    expect(fetch).toHaveBeenCalledWith(
      '/v1/battles/battle%2Fid/events?attempt=2&after=0&limit=50',
      { signal: undefined },
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
})

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}
