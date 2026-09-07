// Loads decision history for the current attempt and refreshes it while the battle remains
// queued or running.

import { useEffect, useState } from 'react'
import { BattleApiError, BattleRun, DecisionHistory, listDecisionHistory } from '../api/battles'
import { eventPollIntervalMs } from './constants'

export function useDecisionHistory(id: string, battle: BattleRun | null) {
  const [decisions, setDecisions] = useState<DecisionHistory[]>([])
  const [message, setMessage] = useState('')
  useEffect(() => {
    if (!battle || battle.attempt < 1) { setDecisions([]); setMessage(''); return }
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    const active = battle.status === 'queued' || battle.status === 'running'
    const attempt = battle.attempt
    async function poll() {
      try {
        setDecisions(await listDecisionHistory(id, attempt, controller.signal))
        setMessage('')
      } catch (error) {
        if (controller.signal.aborted) return
        setMessage(error instanceof BattleApiError ? error.message : 'AI decisions could not be loaded.')
      }
      if (active && !controller.signal.aborted) timer = setTimeout(poll, eventPollIntervalMs)
    }
    void poll()
    return () => { controller.abort(); if (timer) clearTimeout(timer) }
  }, [battle?.attempt, battle?.status, id])
  return { decisions, decisionMessage: message }
}
