import { describe, expect, it } from 'vitest'
import { nextProductionWindow } from './production'

describe('next production window', () => {
  it('returns the first interval multiple after the current tick', () => {
    expect(nextProductionWindow(0, 10)).toEqual({ nextTick: 10, ticksAway: 10 })
    expect(nextProductionWindow(12, 10)).toEqual({ nextTick: 20, ticksAway: 8 })
    expect(nextProductionWindow(19, 10)).toEqual({ nextTick: 20, ticksAway: 1 })
  })

  it('moves past a window once its tick is reached', () => {
    expect(nextProductionWindow(20, 10)).toEqual({ nextTick: 30, ticksAway: 10 })
  })

  it('returns null without a usable interval', () => {
    expect(nextProductionWindow(5, undefined)).toBeNull()
    expect(nextProductionWindow(5, 0)).toBeNull()
  })
})
