// Client for generation availability, idempotent background-draft requests, and job
// polling, including quota details on rejected requests.

import { createApiClient } from '../../../shared/api/request'
export interface BackgroundInput { name: string; doctrine: string; background: string }
export interface GenerationOptions { available: boolean; modelLabel: string; reason?: string; remaining: number; resetAt: string; retryAt: string; activeJobId?: string }
export interface BackgroundJob { id: string; input: BackgroundInput; modelLabel: string; status: 'queued' | 'running' | 'succeeded' | 'failed' | 'expired'; background: string; errorCategory?: string }
export class GenerationError extends Error {
 constructor(message: string, public status?: number, public options?: GenerationOptions) { super(message) }
}
const request = createApiClient(GenerationError)
const path = '/v1/faction-background-generations'
export async function generationOptions(signal?: AbortSignal): Promise<GenerationOptions> {
 const response = await request(`${path}/options`, { signal })
 if (!response.ok) throw new GenerationError('Generation availability could not be loaded.', response.status)
 return response.json()
}
export async function generateBackground(input: BackgroundInput, idempotencyKey: string, signal?: AbortSignal): Promise<BackgroundJob> {
 const response = await request(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ...input, idempotencyKey }), signal })
 if (response.status === 429) throw new GenerationError('Generation limit reached. Wait for the cooldown or daily reset.', 429, await response.json())
 if (!response.ok) throw new GenerationError(response.status === 503 ? 'Background generation is unavailable. You can still write a background.' : 'Background generation could not be queued.', response.status)
 return response.json()
}
export async function backgroundJob(id: string, signal?: AbortSignal): Promise<BackgroundJob> {
 const response = await request(`${path}/${encodeURIComponent(id)}`, { signal })
 if (!response.ok) throw new GenerationError('Background generation status could not be loaded.', response.status)
 return response.json()
}
