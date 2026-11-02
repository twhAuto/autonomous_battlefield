import { afterEach, describe, expect, it, vi } from 'vitest'
import { needsDisplayName, normalizeDisplayName, setDisplayName, signedInName } from './profile'

afterEach(() => vi.unstubAllGlobals())

describe('display-name rules', () => {
  it('trims accepted names and rejects the rest', () => {
    expect(normalizeDisplayName('  Iron Duke ')).toBe('Iron Duke')
    expect(normalizeDisplayName('Grace_Hopper-24 chars ok')).toBe('Grace_Hopper-24 chars ok')
    for (const name of ['Al', '   ', 'Ada!', 'Zoë Smith', 'Twenty-five characters ok']) expect(normalizeDisplayName(name)).toBeNull()
  })

  it('asks only Players and Testers without a name', () => {
    const session = (role?: 'guest' | 'player' | 'tester' | 'admin', displayName?: string) => ({ enabled: true, authenticated: true, actor: { id: 'id', role, displayName } })
    expect(needsDisplayName(session('player'))).toBe(true)
    expect(needsDisplayName(session('tester'))).toBe(true)
    expect(needsDisplayName(session('player', 'Ada'))).toBe(false)
    expect(needsDisplayName(session('admin'))).toBe(false)
    expect(needsDisplayName(session('guest'))).toBe(false)
    expect(needsDisplayName(session())).toBe(false)
  })

  it('names the signed-in person without exposing an ID or email', () => {
    expect(signedInName({ enabled: true, authenticated: true, actor: { id: 'issuer|sub', email: 'ada@example.com', role: 'player', displayName: 'Ada' } })).toBe('Ada')
    expect(signedInName({ enabled: true, authenticated: true, actor: { id: 'issuer|sub', role: 'admin' } })).toBe('Admin')
    expect(signedInName({ enabled: true, authenticated: true, actor: { id: 'local-development' } })).toBe('Admin')
    expect(signedInName({ enabled: true, authenticated: true, actor: { id: 'issuer|sub', role: 'guest' } })).toBe('Guest')
    expect(signedInName({ enabled: true, authenticated: true, mode: 'cloudflare', actor: { id: 'issuer|sub' } })).toBe('Unnamed commander')
  })
})

describe('setDisplayName', () => {
  const respond = (body: string, status: number) => vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(body, { status })))

  it('saves the name and returns the stored value', async () => {
    respond(JSON.stringify({ displayName: 'Iron Duke' }), 200)
    await expect(setDisplayName('Iron Duke')).resolves.toBe('Iron Duke')
    expect(fetch).toHaveBeenCalledWith('/v1/profile', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ displayName: 'Iron Duke' }) }))
  })

  it.each([
    [409, '{"error":{"code":"conflict","message":"that display name is taken"}}', 'That name is taken. Choose another.'],
    [400, '{"error":{"code":"invalid_request","message":"display names are 3 to 24"}}', 'Use 3 to 24 letters, digits, spaces, hyphens or underscores.'],
    [503, '{"error":{"code":"service_unavailable","message":"Your display name could not be saved. Try again."}}', 'Your display name could not be saved. Try again.'],
    [500, '', 'Your display name could not be saved. Try again.'],
  ])('explains a %i response', async (status, body, message) => {
    respond(body, status)
    await expect(setDisplayName('Iron Duke')).rejects.toMatchObject({ message, status })
  })

  it('reports an unreachable server', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('offline')))
    await expect(setDisplayName('Iron Duke')).rejects.toThrow('The command post is unreachable')
  })
})
