// Checks session, login, and logout requests and maps authentication and network failures
// to user-facing errors.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { authenticationClient, AuthenticationError } from './auth'

describe('authenticationClient', () => {
  beforeEach(() => vi.stubGlobal('fetch', vi.fn()))
  afterEach(() => vi.unstubAllGlobals())

  it('reads authenticated and unauthenticated sessions', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(new Response(JSON.stringify({
      enabled: true, authenticated: true, actor: { id: 'admin' },
    }), { status: 200 }))
    await expect(authenticationClient.session()).resolves.toEqual({
      enabled: true, authenticated: true, actor: { id: 'admin' },
    })
    expect(fetch).toHaveBeenLastCalledWith('/v1/auth/session', { credentials: 'same-origin', headers: { 'X-Requested-With': 'XMLHttpRequest' } })

    vi.mocked(fetch).mockResolvedValueOnce(new Response(null, { status: 401 }))
    await expect(authenticationClient.session()).resolves.toEqual({ enabled: true, authenticated: false, mode: 'cloudflare' })

    // The server's own unauthenticated session body is used as-is.
    vi.mocked(fetch).mockResolvedValueOnce(new Response(JSON.stringify({ enabled: true, authenticated: false, mode: 'local' }), { status: 401 }))
    await expect(authenticationClient.session()).resolves.toEqual({ enabled: true, authenticated: false, mode: 'local' })

    // An error envelope is not a session and must not be mistaken for one.
    vi.mocked(fetch).mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: 'authentication_required', message: 'authentication required' } }), { status: 401 }))
    await expect(authenticationClient.session()).resolves.toEqual({ enabled: true, authenticated: false, mode: 'cloudflare' })
  })

  it('logs in and re-reads the signed-cookie session', async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ enabled: true, authenticated: true, actor: { id: 'admin' } }), { status: 200 }))

    await expect(authenticationClient.login('admin', 'password')).resolves.toMatchObject({ authenticated: true })
    expect(fetch).toHaveBeenNthCalledWith(1, '/v1/auth/login', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'content-type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' },
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
    await expect(authenticationClient.logout()).resolves.toBe(false)
    expect(fetch).toHaveBeenCalledWith('/v1/auth/logout', { method: 'POST', credentials: 'same-origin', headers: { 'X-Requested-With': 'XMLHttpRequest' } })

    vi.mocked(fetch).mockResolvedValueOnce(new Response(null, { status: 500 }))
    await expect(authenticationClient.logout()).rejects.toEqual(expect.objectContaining({ status: 500 }))
  })

  it('reports leaving for the Access logout page only for the Access logout URL', async () => {
    const assign = vi.fn()
    vi.stubGlobal('location', { ...window.location, assign })
    const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
    vi.mocked(fetch).mockResolvedValueOnce(json({ logoutUrl: '/cdn-cgi/access/logout' }))
    await expect(authenticationClient.logout()).resolves.toBe(true)
    expect(assign).toHaveBeenCalledWith('/cdn-cgi/access/logout')
    vi.mocked(fetch).mockResolvedValueOnce(json({ logoutUrl: 'https://elsewhere.example/logout' }))
    await expect(authenticationClient.logout()).resolves.toBe(false)
    vi.mocked(fetch).mockResolvedValueOnce(new Response(null, { status: 401 }))
    await expect(authenticationClient.logout()).resolves.toBe(false)
    expect(assign).toHaveBeenCalledOnce()
  })
})
