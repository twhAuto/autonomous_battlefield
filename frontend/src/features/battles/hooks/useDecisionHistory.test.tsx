import { act, renderHook, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import type { BattleRun, DecisionHistory } from '../api/battles'
import { useDecisionHistory } from './useDecisionHistory'

it('clears the previous attempt and ignores its late response', async () => {
 let finish!: (value: Response) => void
 const decision: DecisionHistory = { requestId: 'new', factionId: 'a', targetTick: 0, status: 'accepted', controllerType: 'scripted', usage: {}, createdAt: '' }
 vi.stubGlobal('fetch', vi.fn().mockImplementationOnce(() => new Promise((resolve) => { finish = resolve })).mockResolvedValue(new Response(JSON.stringify({ decisions: [decision] }))))
 const battle = { attempt: 1, status: 'completed' } as BattleRun
 const { result, rerender } = renderHook(({ run }) => useDecisionHistory('battle', run), { initialProps: { run: battle } })
 await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1))
 const signal = vi.mocked(fetch).mock.calls[0][1]?.signal
 rerender({ run: { ...battle, attempt: 2 } })
 expect(result.current.decisions).toEqual([])
 expect(signal?.aborted).toBe(true)
 await waitFor(() => expect(result.current.decisions[0]?.requestId).toBe('new'))
 await act(async () => finish(new Response(JSON.stringify({ decisions: [{ ...decision, requestId: 'old' }] }))))
 expect(result.current.decisions[0]?.requestId).toBe('new')
 rerender({ run: { ...battle, attempt: 0 } })
 expect(result.current.decisions).toEqual([])
})

it('never requests decision history when inspection is disabled and clears existing data', async () => {
 vi.stubGlobal('fetch', vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ decisions: [{requestId:'secret'}] })))))
 const battle = {attempt:1,status:'completed'} as BattleRun
 const {result,rerender}=renderHook(({enabled})=>useDecisionHistory('battle',battle,enabled),{initialProps:{enabled:false}})
 expect(fetch).not.toHaveBeenCalled()
 expect(result.current.decisions).toEqual([])
 rerender({enabled:true})
 await waitFor(()=>expect(result.current.decisions).toHaveLength(1))
 rerender({enabled:false})
 expect(result.current.decisions).toEqual([])
 expect(fetch).toHaveBeenCalledTimes(1)
})
