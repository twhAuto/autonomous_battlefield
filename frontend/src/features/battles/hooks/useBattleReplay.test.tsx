// Checks replay playback: debounced segment loading, prefetching, timed advance and its
// end, stepping, speed, buffering, failures with retry, cache eviction and abort.

import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { BattleApiError, getBattleReplay, type BattleReplay } from '../api/battles'
import { replaySeekDelayMilliseconds, replayTickMilliseconds, useBattleReplay } from './useBattleReplay'

vi.mock('../api/battles', async (original) => ({ ...await original<typeof import('../api/battles')>(), getBattleReplay: vi.fn() }))

// Ten-tick segments from tick 0 to finalTick, as the API returns them.
function segmentAt(tick: number, finalTick: number): BattleReplay {
  const fromTick = Math.floor(tick / 10) * 10
  const toTick = Math.min(fromTick + 9, finalTick)
  return {
    battleId: 'battle', attempt: 1, finalTick, fromTick, toTick, nextTick: toTick < finalTick ? toTick + 1 : null,
    frames: Array.from({ length: toTick - fromTick + 1 }, (_, index) => ({
      tick: fromTick + index, events: [],
      state: { battleId: 'battle', tick: fromTick + index, factions: [], units: [], buildings: [] },
    })),
  }
}

function requestedTicks() {
  return vi.mocked(getBattleReplay).mock.calls.map((call) => call[1])
}

async function wait(milliseconds: number) {
  await act(async () => { await vi.advanceTimersByTimeAsync(milliseconds) })
}

// Each playback timer is scheduled by an effect after the previous tick renders, so
// advance one interval per act.
async function playFor(intervals: number, interval = replayTickMilliseconds) {
  for (let index = 0; index < intervals; index++) await wait(interval)
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.mocked(getBattleReplay).mockReset().mockImplementation(async (_, tick) => segmentAt(tick, 25))
})
afterEach(() => { vi.useRealTimers() })

it('loads the first segment, prefetches the next and plays to the final tick', async () => {
  const view = renderHook(() => useBattleReplay('battle', 25))
  expect(view.result.current.frame).toBeNull()
  await wait(replaySeekDelayMilliseconds)
  expect(view.result.current.frame?.tick).toBe(0)
  expect(requestedTicks()).toEqual([0, 10])

  act(() => view.result.current.play())
  await playFor(12)
  expect(view.result.current.tick).toBe(12)
  expect(requestedTicks()).toEqual([0, 10, 20])
  await playFor(20)
  expect(view.result.current.tick).toBe(25)
  expect(view.result.current.playing).toBe(false)

  act(() => view.result.current.play())
  expect(view.result.current.tick).toBe(0)
  expect(view.result.current.playing).toBe(true)
})

it('fetches only where a dragged timeline stops', async () => {
  const view = renderHook(() => useBattleReplay('battle', 25))
  act(() => view.result.current.seek(4))
  act(() => view.result.current.seek(14))
  act(() => view.result.current.seek(99))
  await wait(replaySeekDelayMilliseconds)
  expect(view.result.current.tick).toBe(25)
  expect(view.result.current.frame?.tick).toBe(25)
  expect(requestedTicks()).toEqual([25])
})

it('steps, clamps and speeds up playback', async () => {
  const view = renderHook(() => useBattleReplay('battle', 25))
  await wait(replaySeekDelayMilliseconds)
  act(() => view.result.current.step(-1))
  expect(view.result.current.tick).toBe(0)
  act(() => view.result.current.play())
  act(() => view.result.current.step(2))
  expect(view.result.current).toMatchObject({ tick: 2, playing: false })

  act(() => view.result.current.setSpeed(4))
  act(() => view.result.current.play())
  await playFor(4, replayTickMilliseconds / 4)
  expect(view.result.current.tick).toBe(6)
  act(() => view.result.current.pause())
  await wait(replayTickMilliseconds)
  expect(view.result.current.tick).toBe(6)
})

it('buffers while the next segment is still loading', async () => {
  let release!: () => void
  vi.mocked(getBattleReplay).mockImplementation(async (_, tick) => {
    if (tick === 10) await new Promise<void>((resolve) => { release = resolve })
    return segmentAt(tick, 25)
  })
  const view = renderHook(() => useBattleReplay('battle', 25))
  await wait(replaySeekDelayMilliseconds)
  act(() => view.result.current.seek(9))
  act(() => view.result.current.play())
  await wait(replayTickMilliseconds)
  expect(view.result.current).toMatchObject({ tick: 9, buffering: true })
  await act(async () => { release() })
  await wait(replayTickMilliseconds)
  expect(view.result.current).toMatchObject({ tick: 10, buffering: false })
})

it('reports a failed segment, stops playback and retries on request', async () => {
  vi.mocked(getBattleReplay)
    .mockRejectedValueOnce(new BattleApiError('No replay was recorded.', 409))
    .mockRejectedValueOnce(new TypeError('offline'))
  const view = renderHook(() => useBattleReplay('battle', 25))
  await wait(replaySeekDelayMilliseconds)
  expect(view.result.current.error).toBe('No replay was recorded.')
  await wait(replaySeekDelayMilliseconds * 4)
  expect(requestedTicks()).toEqual([0])

  act(() => view.result.current.retry())
  await wait(replaySeekDelayMilliseconds)
  expect(view.result.current.error).toBe('The battle replay could not be loaded.')
  act(() => view.result.current.retry())
  await wait(replaySeekDelayMilliseconds)
  expect(view.result.current.error).toBe('')
  expect(view.result.current.frame?.tick).toBe(0)
})

it('drops far segments from memory and reloads them when revisited', async () => {
  vi.mocked(getBattleReplay).mockImplementation(async (_, tick) => segmentAt(tick, 95))
  const view = renderHook(() => useBattleReplay('battle', 95))
  for (const tick of [0, 20, 40, 60, 80, 90]) {
    act(() => view.result.current.seek(tick))
    await wait(replaySeekDelayMilliseconds)
  }
  vi.mocked(getBattleReplay).mockClear()
  act(() => view.result.current.seek(0))
  await wait(replaySeekDelayMilliseconds)
  expect(requestedTicks()).toContain(0)
  expect(view.result.current.frame?.tick).toBe(0)
})

it('aborts outstanding requests when unmounted', async () => {
  vi.mocked(getBattleReplay).mockImplementation(() => new Promise(() => {}))
  const view = renderHook(() => useBattleReplay('battle', 25))
  await wait(replaySeekDelayMilliseconds)
  const signal = vi.mocked(getBattleReplay).mock.calls[0][2]
  view.unmount()
  expect(signal?.aborted).toBe(true)
})
