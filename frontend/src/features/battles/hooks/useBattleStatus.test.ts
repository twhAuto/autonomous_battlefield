import { act, renderHook } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { getBattle, type BattleRun } from '../api/battles'
import { useBattleStatus } from './useBattleStatus'

vi.mock('../api/battles', async (original) => ({ ...await original<typeof import('../api/battles')>(), getBattle: vi.fn() }))

it('ignores an in-flight status response and stops polling after an operator stop', async () => {
  vi.useFakeTimers()
  const running = { id: 'battle', status: 'running' } as BattleRun
  let resolvePoll!: (battle: BattleRun) => void
  vi.mocked(getBattle).mockResolvedValueOnce(running).mockImplementationOnce(() => new Promise((resolve) => { resolvePoll = resolve }))
  const view = renderHook(() => useBattleStatus('battle'))
  try {
    await act(async () => {})
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    expect(getBattle).toHaveBeenCalledTimes(2)
    const signal = vi.mocked(getBattle).mock.calls[1][1]
    act(() => view.result.current.applyBattle({ ...running, status: 'stopped' }))
    expect(signal?.aborted).toBe(true)
    // Even a transport that resolves after abort cannot overwrite the control result.
    await act(async () => { resolvePoll(running) })
    expect(view.result.current.battle?.status).toBe('stopped')
    expect(view.result.current.connection).toBe('settled')
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(getBattle).toHaveBeenCalledTimes(2)
  } finally {
    view.unmount()
    vi.useRealTimers()
  }
})

it('records when battle data last arrived and keeps it while reconnecting', async () => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-09-29T10:00:00Z'))
  const running = { id: 'battle', status: 'running' } as BattleRun
  vi.mocked(getBattle).mockReset().mockResolvedValueOnce(running).mockRejectedValue(new Error('offline'))
  const view = renderHook(() => useBattleStatus('battle'))
  try {
    expect(view.result.current.lastUpdated).toBeNull()
    await act(async () => {})
    expect(view.result.current.lastUpdated?.toISOString()).toBe('2026-09-29T10:00:00.000Z')
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    expect(view.result.current.connection).toBe('reconnecting')
    expect(view.result.current.lastUpdated?.toISOString()).toBe('2026-09-29T10:00:00.000Z')
  } finally {
    view.unmount()
    vi.useRealTimers()
  }
})
