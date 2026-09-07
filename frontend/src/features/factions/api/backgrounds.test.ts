// Checks generation availability, idempotent submission, job reads, and transport error
// handling.

import { beforeEach, expect, it, vi } from 'vitest'
import { backgroundJob, generateBackground, generationOptions } from './backgrounds'
const input = { name: 'A', doctrine: 'Defend', background: '' }
beforeEach(() => vi.stubGlobal('fetch', vi.fn()))
it('loads options, submits an idempotent draft and reads its job', async () => {
 vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify({ available: true }))); await expect(generationOptions()).resolves.toEqual({ available: true })
 vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify({ id: 'job' }), { status: 202 })); await expect(generateBackground(input, 'key')).resolves.toEqual({ id: 'job' }); expect(fetch).toHaveBeenLastCalledWith('/v1/faction-background-generations', expect.objectContaining({ body: JSON.stringify({ ...input, idempotencyKey: 'key' }) }))
 vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify({ status: 'running' }))); await expect(backgroundJob('job')).resolves.toEqual({ status: 'running' })
})
it.each([429, 503, 500])('handles admission status %d', async status => {
 vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify({ remaining: 0 }), { status })); await expect(generateBackground(input, 'key')).rejects.toMatchObject({ status, ...(status === 429 ? { options: { remaining: 0 } } : {}) })
})
it('handles failed option/status reads and network errors', async () => {
 vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 500 })); await expect(generationOptions()).rejects.toMatchObject({ status: 500 }); await expect(backgroundJob('job')).rejects.toMatchObject({ status: 500 })
 vi.mocked(fetch).mockRejectedValue(new TypeError('offline')); await expect(generateBackground(input, 'key')).rejects.toThrow(/unreachable/)
})
