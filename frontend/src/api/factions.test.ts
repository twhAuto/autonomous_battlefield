import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createFaction, FactionApiError, listFactions } from './factions'

const faction = { id: '7f22e693-bacd-46bb-b942-64fa613cd388', name: 'Northwind' }

describe('faction API', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })

  it('lists factions', async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse({ factions: [faction] }))

    await expect(listFactions()).resolves.toEqual([faction])
    expect(fetch).toHaveBeenCalledWith('/v1/factions', { signal: undefined })
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
    [500, 'faction could not be created'],
  ])('maps create status %d', async (status, message) => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status }))
    await expect(createFaction(faction)).rejects.toMatchObject({
      name: 'FactionApiError',
      status,
      message: expect.stringContaining(message),
    })
  })

  it('maps create network failures', async () => {
    vi.mocked(fetch).mockRejectedValue(new TypeError('offline'))
    await expect(createFaction(faction)).rejects.toEqual(new FactionApiError(
      'The command post is unreachable. Check that the battlefield server is running.',
    ))
  })
})

function jsonResponse(value: unknown) {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}
