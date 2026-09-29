import { describe, expect, it } from 'vitest'
import type { BattleRun } from '../api/battles'
import { productionSummary } from './production'

const running = { status: 'running', paused: false, currentTick: 12, state: { tick: 12 } } as BattleRun

describe('production summary', () => {
  it('counts down to the next regular window', () => {
    expect(productionSummary({ ...running, production: { nextTick: 20, opening: false } })).toBe('Next production window: tick 20 (in 8 ticks) · up to 6 units per faction')
    expect(productionSummary({ ...running, production: { nextTick: 13, opening: false } })).toBe('Next production window: tick 13 (in 1 tick) · up to 6 units per faction')
  })

  it('reports a pending opening purchase and a paused battle', () => {
    expect(productionSummary({ ...running, status: 'queued', currentTick: 0, state: { tick: 0 } as BattleRun['state'], production: { nextTick: 0, opening: true } })).toBe('Opening purchase pending (before tick 1) · up to 6 units per faction')
    expect(productionSummary({ ...running, paused: true, production: { nextTick: 20, opening: false } })).toContain('(in 8 ticks, paused)')
  })

  it('says when no window remains before the tick limit', () => {
    expect(productionSummary({ ...running, production: null })).toBe('No production windows remain before the tick limit')
  })

  it('shows nothing for settled battles or responses without the field', () => {
    expect(productionSummary({ ...running, status: 'completed', production: null })).toBeNull()
    expect(productionSummary(running)).toBeNull()
  })
})
