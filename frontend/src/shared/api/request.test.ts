import { describe, expect, it, vi } from 'vitest'
import { apiFetch, authenticationRequiredEvent } from './request'

describe('apiFetch', () => {
  it('announces unauthorized API responses without changing the response', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 401 })))
    const listener = vi.fn()
    window.addEventListener(authenticationRequiredEvent, listener)

    const response = await apiFetch('/v1/factions')

    expect(response.status).toBe(401)
    expect(listener).toHaveBeenCalledOnce()
    window.removeEventListener(authenticationRequiredEvent, listener)
  })
})
