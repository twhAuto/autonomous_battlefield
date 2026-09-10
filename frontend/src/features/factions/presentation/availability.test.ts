import { describe, expect, it } from 'vitest'
import { generationAvailability } from './availability'

describe('generation availability', () => {
  const options = { available: true, modelLabel: 'Writer', remaining: 3, resetAt: '', retryAt: '' }
  it('distinguishes failed reads, unavailable service, quota, cooldown and active work', () => {
    expect(generationAvailability(undefined, true, false, false, 0)).toBe('loading')
    expect(generationAvailability(undefined, false, false, false, 0)).toBe('unknown')
    expect(generationAvailability(options, false, true, false, 0)).toBe('unknown')
    expect(generationAvailability({ ...options, available: false, remaining: 0 }, false, false, false, 0)).toBe('unavailable')
    expect(generationAvailability({ ...options, remaining: 0 }, false, false, false, 0)).toBe('quota')
    expect(generationAvailability({ ...options, retryAt: '2026-09-10T00:00:00Z' }, false, false, false, 0)).toBe('cooldown')
    expect(generationAvailability({ ...options, remaining: 0 }, true, false, true, 0)).toBe('active')
    expect(generationAvailability(options, false, false, false, 0)).toBe('available')
  })
})
