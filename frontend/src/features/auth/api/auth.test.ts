import { beforeEach, describe, expect, it, vi } from 'vitest'
import { authenticationClient, AuthenticationError } from './auth'

describe('authenticationClient', () => {
  beforeEach(() => vi.stubGlobal('fetch', vi.fn()))

  it('reads authenticated and unauthenticated sessions', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(new Response(JSON.stringify({
      enabled: true, authenticated: true, actor: { id: 'admin' },
    }), { status: 200 }))
    await expect(authenticationClient.session()).resolves.toEqual({
      enabled: true, authenticated: true, actor: { id: 'admin' },
    })
    expect(fetch).toHaveBeenLastCalledWith('/v1/auth/session', { credentials: 'same-origin' })

    vi.mocked(fetch).mockResolvedValueOnce(new Response(null, { status: 401 }))
    await expect(authenticationClient.session()).resolves.toEqual({ enabled: true, authenticated: false })
  })

  it('logs in and re-reads the signed-cookie session', async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ enabled: true, authenticated: true, actor: { id: 'admin' } }), { status: 200 }))

    await expect(authenticationClient.login('admin', 'password')).resolves.toMatchObject({ authenticated: true })
    expect(fetch).toHaveBeenNthCalledWith(1, '/v1/auth/login', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: 'admin', password: 'password' }),
    })
  })

  it('reports invalid credentials and unavailable authentication endpoints', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(new Response(null, { status: 401 }))
    await expect(authenticationClient.login('admin', 'wrong')).rejects.toEqual(expect.objectContaining({
      name: 'AuthenticationError', status: 401,
    }))

    vi.mocked(fetch).mockRejectedValueOnce(new TypeError('offline'))
    await expect(authenticationClient.session()).rejects.toBeInstanceOf(AuthenticationError)

    vi.mocked(fetch).mockResolvedValueOnce(new Response(null, { status: 500 }))
    await expect(authenticationClient.session()).rejects.toEqual(expect.objectContaining({ status: 500 }))
  })

  it('logs out and reports logout failures', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(new Response(null, { status: 204 }))
    await expect(authenticationClient.logout()).resolves.toBeUndefined()
    expect(fetch).toHaveBeenCalledWith('/v1/auth/logout', { method: 'POST', credentials: 'same-origin' })

    vi.mocked(fetch).mockResolvedValueOnce(new Response(null, { status: 500 }))
    await expect(authenticationClient.logout()).rejects.toEqual(expect.objectContaining({ status: 500 }))
  })
})
