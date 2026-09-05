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

it('normalizes the base URL and forwards request options', async () => {
  vi.stubEnv('VITE_API_URL', 'https://battlefield.example/')
  vi.resetModules()
  const { apiFetch: configuredFetch } = await import('./request')
  const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetch)
  const signal = new AbortController().signal
  try {
    await configuredFetch('/v1/battles', { method: 'POST', signal })
    expect(fetch).toHaveBeenCalledWith('https://battlefield.example/v1/battles', { method: 'POST', signal })
  } finally {
    vi.unstubAllEnvs()
    vi.resetModules()
  }
})

it('keeps authentication failures local when notifications are disabled', async () => {
  const { createApiClient } = await import('./request')
  const listener = vi.fn()
  window.addEventListener(authenticationRequiredEvent, listener)
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 401 })))
  try {
    const response = await createApiClient(Error, false)('/v1/auth/login')
    expect(response.status).toBe(401)
    expect(listener).not.toHaveBeenCalled()
  } finally {
    window.removeEventListener(authenticationRequiredEvent, listener)
  }
})

it('preserves cancellation and maps network errors to the feature error type', async () => {
  const { createApiClient } = await import('./request')
  class FeatureError extends Error {}
  const client = createApiClient(FeatureError)
  const aborted = new DOMException('aborted', 'AbortError')
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(aborted))
  await expect(client('/v1/battles')).rejects.toBe(aborted)
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('offline')))
  await expect(client('/v1/battles')).rejects.toBeInstanceOf(FeatureError)
})
