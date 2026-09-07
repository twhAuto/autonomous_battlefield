// Polls battle status until it settles, with bounded exponential reconnects, manual retry,
// and updates from battle controls.

import { useCallback, useEffect, useRef, useState } from 'react'
import { BattleApiError, BattleRun, getBattle } from '../api/battles'
import { type ConnectionState } from '../types'

/** Tracks one battle, stopping automatic polling at terminal status or after bounded reconnect failures. */
export function useBattleStatus(id: string) {
  const [battle, setBattle] = useState<BattleRun | null>(null)
  const [connection, setConnection] = useState<ConnectionState>('connecting')
  const [connectionMessage, setConnectionMessage] = useState('')
  const [retryVersion, setRetryVersion] = useState(0)
  const stopPolling = useRef<() => void>(() => {})

  useEffect(() => {
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    let failures = 0
    let stopped = false

    async function poll() {
      try {
        const current = await getBattle(id, controller.signal)
        if (stopped) return
        failures = 0
        setBattle(current)
        setConnectionMessage('')
        if (current.status === 'queued' || current.status === 'running') {
          setConnection('live')
          timer = setTimeout(poll, 1000)
        } else {
          setConnection('settled')
        }
      } catch (error) {
        if (controller.signal.aborted || stopped) return
        failures += 1
        const message = error instanceof BattleApiError ? error.message : 'Battle status could not be loaded.'
        if (failures <= 5) {
          setConnection('reconnecting')
          setConnectionMessage(`${message} Reconnecting ${failures}/5…`)
          timer = setTimeout(poll, Math.min(1000 * 2 ** (failures - 1), 10_000))
        } else {
          setConnection('error')
          setConnectionMessage(message)
        }
      }
    }

    setConnection('connecting')
    setConnectionMessage('')
    void poll()
    const cancel = () => {
      stopped = true
      controller.abort()
      if (timer) clearTimeout(timer)
    }
    stopPolling.current = cancel
    return cancel
  }, [id, retryVersion])

  const retryConnection = useCallback(() => setRetryVersion((value) => value + 1), [])
  const applyBattle = useCallback((current: BattleRun) => {
    if (current.status !== 'queued' && current.status !== 'running') stopPolling.current()
    setBattle(current)
    setConnection(current.status === 'queued' || current.status === 'running' ? 'live' : 'settled')
    setConnectionMessage('')
  }, [])
  return { battle, connection, connectionMessage, retryConnection, applyBattle }
}
