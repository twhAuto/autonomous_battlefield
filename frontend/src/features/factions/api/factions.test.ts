import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createFaction, deleteFaction, FactionApiError, listFactionRulesets, listFactions } from './factions'

const faction = {
  id: '7f22e693-bacd-46bb-b942-64fa613cd388', name: 'Northwind', doctrine: 'Defend your castle.',
}

describe('faction API', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })

  it('lists factions', async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse({ factions: [faction] }))

    await expect(listFactions()).resolves.toEqual([faction])
    expect(fetch).toHaveBeenCalledWith('/v1/factions', { signal: undefined })
  })

  it('discovers faction rulesets', async () => {
    const ruleset = { id: 'castle-siege-v1', version: '1', designBudget: 120, units: [], upgrades: [] }
    vi.mocked(fetch).mockResolvedValue(jsonResponse({ rulesets: [ruleset] }))
    await expect(listFactionRulesets()).resolves.toEqual([ruleset])
    expect(fetch).toHaveBeenCalledWith('/v1/faction-rulesets', { signal: undefined })
  })

  it('maps list HTTP and network failures', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(new Response(null, { status: 503 }))
    await expect(listFactions()).rejects.toMatchObject({
      name: 'FactionApiError',
      status: 503,
      message: expect.stringContaining('registry could not be loaded'),
    })

    vi.mocked(fetch).mockRejectedValueOnce(new TypeError('offline'))
    await expect(listFactions()).rejects.toThrow('The command post is unreachable')

    const abort = new DOMException('aborted', 'AbortError')
    vi.mocked(fetch).mockRejectedValueOnce(abort)
    await expect(listFactions()).rejects.toBe(abort)
  })

  it('creates a faction', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 201 }))

    await expect(createFaction(faction)).resolves.toBeUndefined()
    expect(fetch).toHaveBeenCalledWith('/v1/factions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(faction),
    })
  })

  it.each([
    [409, 'identifier is already in use'],
    [400, 'server rejected these faction details'],
    [422, 'faction details are invalid'],
    [500, 'faction could not be created'],
  ])('maps create status %d', async (status, message) => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status }))
    await expect(createFaction(faction)).rejects.toMatchObject({
      name: 'FactionApiError',
      status,
      message: expect.stringContaining(message),
    })
  })

  it('returns the blueprint validation detail supplied by the server', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(
      'invalid faction blueprint: unknown ruleset ""\n',
      { status: 422 },
    ))

    await expect(createFaction(faction)).rejects.toMatchObject({
      status: 422,
      message: 'invalid faction blueprint: unknown ruleset ""',
    })
  })

  it('maps create network failures', async () => {
    vi.mocked(fetch).mockRejectedValue(new TypeError('offline'))
    await expect(createFaction(faction)).rejects.toEqual(new FactionApiError(
      'The command post is unreachable. Check that the battlefield server is running.',
    ))
  })

  it('deletes a faction', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 204 }))
    await expect(deleteFaction(faction.id)).resolves.toBeUndefined()
    expect(fetch).toHaveBeenCalledWith(`/v1/factions/${faction.id}`, { method: 'DELETE' })
  })

  it.each([[404, 'no longer exists'], [400, 'identifier is invalid'], [500, 'could not be deleted']])(
    'maps delete status %d', async (status, message) => {
      vi.mocked(fetch).mockResolvedValue(new Response(null, { status }))
      await expect(deleteFaction(faction.id)).rejects.toMatchObject({ status, message: expect.stringContaining(message) })
    },
  )

  it('maps delete network failures', async () => {
    vi.mocked(fetch).mockRejectedValue(new TypeError('offline'))
    await expect(deleteFaction(faction.id)).rejects.toThrow('command post is unreachable')
  })
})

function jsonResponse(value: unknown) {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}
