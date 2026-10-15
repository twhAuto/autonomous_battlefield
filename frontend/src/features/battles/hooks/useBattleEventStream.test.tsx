import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { BattleApiError, listBattleEvents, type BattleEventPage, type BattleRun } from '../api/battles'
import { useBattleEventStream } from './useBattleEventStream'

vi.mock('../api/battles', async original => ({ ...await original<typeof import('../api/battles')>(), listBattleEvents: vi.fn() }))
function page(attempt: number, start: number, count = 1): BattleEventPage {
  return { attempt, nextCursor: start + count - 1, events: Array.from({ length: count }, (_, index) => ({ attempt, sequence: start + index, tick: start + index, type: 'tick_completed', data: {}, createdAt: '' })) }
}
beforeEach(() => { vi.mocked(listBattleEvents).mockReset() })
afterEach(() => { vi.useRealTimers() })

it('ignores late initial and manual responses after changing attempts', async () => {
  let finish!: (value: BattleEventPage) => void
  vi.mocked(listBattleEvents).mockImplementationOnce(() => new Promise(resolve => { finish = resolve })).mockResolvedValue(page(1, 1))
  const { result } = renderHook(() => useBattleEventStream('battle', 2, 'completed'))
  act(() => result.current.setSelectedAttempt(1))
  await waitFor(() => expect(result.current.events).toHaveLength(1))
  await act(async () => finish(page(2, 99)))
  expect(result.current.events.map(event => event.sequence)).toEqual([1])

  vi.mocked(listBattleEvents).mockImplementationOnce(() => new Promise(resolve => { finish = resolve })).mockResolvedValue(page(2, 2))
  let pending!: Promise<void>
  act(() => { pending = result.current.loadEventPage(1, false) })
  act(() => result.current.setSelectedAttempt(2))
  await waitFor(() => expect(result.current.events[0]?.sequence).toBe(2))
  await act(async () => { finish(page(1, 100)); await pending })
  expect(result.current.events.map(event => event.sequence)).toEqual([2])
  expect(result.current.events[0].attempt).toBe(2)
})

it('fetches and drains the final events when status becomes terminal', async () => {
  vi.useFakeTimers()
  vi.mocked(listBattleEvents).mockResolvedValueOnce(page(1, 1))
    .mockResolvedValueOnce(page(1, 2, 50)).mockResolvedValueOnce(page(1, 52))
  const { result, rerender } = renderHook(({ status }: { status: BattleRun['status'] }) => useBattleEventStream('battle', 1, status), { initialProps: { status: 'running' as BattleRun['status'] } })
  await act(async () => {})
  rerender({ status: 'completed' })
  await act(async () => { await vi.advanceTimersByTimeAsync(1) })
  expect(result.current.events.at(-1)?.sequence).toBe(52)
  expect(listBattleEvents).toHaveBeenLastCalledWith('battle', expect.objectContaining({ after: 51, latest: false }), expect.any(AbortSignal))
  await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
  expect(listBattleEvents).toHaveBeenCalledTimes(3)
})

it('serializes manual refresh with polling and retains a bounded deduplicated buffer', async () => {
  vi.useFakeTimers()
  let finish!: (value: BattleEventPage) => void
  vi.mocked(listBattleEvents).mockResolvedValueOnce(page(1, 1, 50))
  for (let start = 51; start < 500; start += 50) vi.mocked(listBattleEvents).mockResolvedValueOnce(page(1, start, 50))
  vi.mocked(listBattleEvents).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  const { result } = renderHook(() => useBattleEventStream('battle', 1, 'running'))
  await act(async () => {})
  for (let cursor = 50; cursor < 500; cursor += 50) await act(async () => result.current.loadEventPage(cursor, false))
  let pending!: Promise<void>
  act(() => { pending = result.current.loadEventPage(500, false) })
  await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
  expect(listBattleEvents).toHaveBeenCalledTimes(11)
  await act(async () => { finish(page(1, 500, 2)); await pending })
  expect(result.current.events).toHaveLength(500)
  expect(result.current.events[0].sequence).toBe(2)
  expect(result.current.events.at(-1)?.sequence).toBe(501)
})

it('retains events on failure and supports a manual retry', async () => {
  vi.mocked(listBattleEvents).mockResolvedValueOnce(page(1, 1))
    .mockRejectedValueOnce(new BattleApiError('Offline', 503)).mockResolvedValueOnce(page(1, 2))
  const { result } = renderHook(() => useBattleEventStream('battle', 1, 'completed'))
  await waitFor(() => expect(result.current.events).toHaveLength(1))
  await act(async () => result.current.loadEventPage(1, false))
  expect(result.current.eventState).toBe('error')
  expect(result.current.eventMessage).toBe('Offline')
  expect(result.current.events).toHaveLength(1)
  await act(async () => result.current.loadEventPage(1, false))
  expect(result.current.eventState).toBe('idle')
  expect(result.current.events.map(event => event.sequence)).toEqual([1, 2])
})

it('ignores an obsolete rejection and does not poll a historical attempt', async () => {
  vi.useFakeTimers()
  let reject!: (error: Error) => void
  vi.mocked(listBattleEvents).mockImplementationOnce(() => new Promise((_, fail) => { reject = fail }))
    .mockResolvedValueOnce(page(1, 1))
  const { result } = renderHook(() => useBattleEventStream('battle', 2, 'running'))
  act(() => result.current.setSelectedAttempt(1))
  await act(async () => {})
  await act(async () => reject(new Error('Old failure')))
  expect(result.current.eventMessage).toBe('')
  expect(result.current.events[0].attempt).toBe(1)
  await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
  expect(listBattleEvents).toHaveBeenCalledTimes(2)
})

it('retries the latest page after an initial load failure, then resumes incrementally', async () => {
  vi.mocked(listBattleEvents).mockRejectedValueOnce(new BattleApiError('Offline', 503))
    .mockResolvedValueOnce(page(1, 900, 50)).mockResolvedValueOnce(page(1, 950))
  const { result } = renderHook(() => useBattleEventStream('battle', 1, 'completed'))
  await waitFor(() => expect(result.current.eventState).toBe('error'))
  await act(async () => result.current.loadEventPage(0, false))
  expect(listBattleEvents).toHaveBeenLastCalledWith('battle', expect.objectContaining({ latest: true }), expect.any(AbortSignal))
  expect(result.current.events[0].sequence).toBe(900)
  expect(result.current.eventHasMore).toBe(false)
  await act(async () => result.current.loadEventPage(result.current.eventCursor, false))
  expect(listBattleEvents).toHaveBeenLastCalledWith('battle', expect.objectContaining({ after: 949, latest: false }), expect.any(AbortSignal))
  expect(result.current.events.at(-1)?.sequence).toBe(950)
})
