import type { GenerationOptions } from '../api/backgrounds'

export function generationAvailability(options: GenerationOptions | undefined, loading: boolean, failed: boolean, active: boolean, now: number) {
  if (active) return 'active'
  if (loading) return 'loading'
  if (failed || !options) return 'unknown'
  if (!options.available) return 'unavailable'
  if (options.remaining <= 0) return 'quota'
  if (Date.parse(options.retryAt) > now) return 'cooldown'
  return 'available'
}
