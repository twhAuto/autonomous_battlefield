// Refreshes one faction's filtered view, retaining it across ticks but never across
// battle, attempt, or perspective changes.
import { useEffect, useState } from 'react'
import { BattleRun, BattleView, getBattleView } from '../api/battles'

export function useFactionPerspective(id: string, battle: BattleRun | null, perspective: string) {
  const scope = `${id}:${battle?.attempt}:${perspective}`
  const enabled = battle !== null && perspective !== 'spectator'
  const [result, setResult] = useState<{ scope: string; view: BattleView | null; message: string }>({ scope: '', view: null, message: '' })
  const [retryVersion, setRetryVersion] = useState(0)
  useEffect(() => {
    if (!enabled) return
    const controller = new AbortController()
    setResult(current => ({ scope, view: current.scope === scope ? current.view : null, message: '' }))
    getBattleView(id, perspective, controller.signal).then(view => {
      if (!controller.signal.aborted) setResult({ scope, view, message: '' })
    }).catch(() => {
      if (!controller.signal.aborted) setResult({ scope, view: null, message: 'Faction view could not be loaded.' })
    })
    return () => controller.abort()
  }, [battle?.currentTick, enabled, id, perspective, scope, retryVersion])
  return {
    view: enabled && result.scope === scope ? result.view : null,
    message: enabled && result.scope === scope ? result.message : '',
    retry: () => setRetryVersion(value => value + 1),
  }
}
