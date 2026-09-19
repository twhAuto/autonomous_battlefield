import { afterEach, expect, it, vi } from 'vitest'
import { getMembers, grantMember, removeMember, retryMembers } from './members'
afterEach(() => vi.unstubAllGlobals())
it('maps server failures to actionable messages', async () => {
  for (const [status, message] of [[400, 'valid email'], [409, 'Administrator'], [404, 'no longer exists'], [503, 'configured']] as const) {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status })))
    await expect(grantMember('bad')).rejects.toThrow(message)
  }
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('network')))
  await expect(getMembers()).rejects.toThrow('unreachable')
})
it('uses encoded member IDs and sends no privileged role assignment', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 202 })))
  await removeMember('a/b'); await retryMembers(); await grantMember('friend@example.com')
  expect(fetch).toHaveBeenCalledWith('/v1/admin/members/a%2Fb', expect.objectContaining({ method: 'DELETE' }))
  expect(fetch).toHaveBeenLastCalledWith('/v1/admin/members', expect.objectContaining({ body: '{"email":"friend@example.com"}' }))
})
