import { useEffect, useState } from 'react'
import { BattleRun, BattleView, getBattleView } from '../api/battles'

export function useFactionPerspective(id: string, battle: BattleRun | null, perspective: string) {
  const [view, setView] = useState<BattleView | null>(null)
  useEffect(() => {
    if (!battle || perspective === 'spectator') { setView(null); return }
    const controller = new AbortController()
    getBattleView(id, perspective, controller.signal).then(setView).catch(() => setView(null))
    return () => controller.abort()
  }, [battle?.currentTick, id, perspective])
  return view
}
