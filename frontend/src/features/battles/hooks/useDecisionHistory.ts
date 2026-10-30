// Loads current-attempt history and rejects responses belonging to an obsolete scope.
import { useEffect, useState } from 'react'
import { BattleApiError, BattleRun, DecisionHistory, listDecisionHistory } from '../api/battles'
import { eventPollIntervalMs } from './constants'

export function useDecisionHistory(id: string, battle: BattleRun | null) {
  const scope = `${id}:${battle?.attempt ?? 0}`
  const [history, setHistory] = useState<{ scope: string; decisions: DecisionHistory[]; message: string }>({ scope: '', decisions: [], message: '' })
  useEffect(() => {
    if (!battle || battle.attempt < 1) return
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    const active = battle.status === 'queued' || battle.status === 'running'
    const attempt = battle.attempt
    async function poll() {
      try {
        const decisions = await listDecisionHistory(id, attempt, controller.signal)
        if (controller.signal.aborted) return
        setHistory({ scope, decisions, message: '' })
      } catch (error) {
        if (controller.signal.aborted) return
        const message = error instanceof BattleApiError ? error.message : 'AI decisions could not be loaded.'
        setHistory((current) => ({ scope, decisions: current.scope === scope ? current.decisions : [], message }))
      }
      if (active && !controller.signal.aborted) timer = setTimeout(poll, eventPollIntervalMs)
    }
    void poll()
    return () => { controller.abort(); if (timer) clearTimeout(timer) }
  }, [battle?.attempt, battle?.status, id, scope])
  return { decisions: history.scope === scope ? history.decisions : [], decisionMessage: history.scope === scope ? history.message : '' }
}
