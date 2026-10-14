import { act, renderHook, waitFor } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { getBattleView, type BattleRun, type BattleView } from '../api/battles'
import { useFactionPerspective } from './useFactionPerspective'

vi.mock('../api/battles', () => ({ getBattleView: vi.fn() }))
const battle = { attempt: 1, currentTick: 5 } as BattleRun
const northView = { visiblePositions: [{ x: 1, y: 1 }] } as BattleView
const southView = { visiblePositions: [{ x: 2, y: 2 }] } as BattleView
beforeEach(() => { vi.mocked(getBattleView).mockReset() })

it('ignores late responses from a previous perspective', async () => {
  let finishNorth!: (view: BattleView) => void
  vi.mocked(getBattleView).mockImplementationOnce(() => new Promise(resolve => { finishNorth = resolve }))
    .mockResolvedValueOnce(southView)
  const { result, rerender } = renderHook(({ perspective }) => useFactionPerspective('battle', battle, perspective), { initialProps: { perspective: 'north' } })
  rerender({ perspective: 'south' })
  await waitFor(() => expect(result.current.view).toBe(southView))
  await act(async () => finishNorth(northView))
  expect(result.current.view).toBe(southView)
})

it('does not clear the new view when an old request rejects after cancellation', async () => {
  let rejectNorth!: (error: Error) => void
  vi.mocked(getBattleView).mockImplementationOnce(() => new Promise((_, reject) => { rejectNorth = reject }))
    .mockResolvedValueOnce(southView)
  const { result, rerender } = renderHook(({ perspective }) => useFactionPerspective('battle', battle, perspective), { initialProps: { perspective: 'north' } })
  rerender({ perspective: 'south' })
  await waitFor(() => expect(result.current.view).toBe(southView))
  await act(async () => rejectNorth(new DOMException('Aborted', 'AbortError')))
  expect(result.current.view).toBe(southView)
  expect(result.current.message).toBe('')
})

it('clears the prior scope immediately and reloads for a new attempt at the same tick', async () => {
  vi.mocked(getBattleView).mockResolvedValueOnce(northView).mockImplementation(() => new Promise(() => {}))
  const { result, rerender } = renderHook(({ attempt }) => useFactionPerspective('battle', { ...battle, attempt }, 'north'), { initialProps: { attempt: 1 } })
  await waitFor(() => expect(result.current.view).toBe(northView))
  rerender({ attempt: 2 })
  expect(result.current.view).toBeNull()
  expect(getBattleView).toHaveBeenCalledTimes(2)
})

it('reports failure and lets a settled battle retry without changing perspective', async () => {
  vi.mocked(getBattleView).mockRejectedValueOnce(new Error('Offline')).mockResolvedValueOnce(northView)
  const { result } = renderHook(() => useFactionPerspective('battle', battle, 'north'))
  await waitFor(() => expect(result.current.message).toBe('Faction view could not be loaded.'))
  expect(result.current.view).toBeNull()
  act(() => result.current.retry())
  await waitFor(() => expect(result.current.view).toBe(northView))
  expect(result.current.message).toBe('')
})
