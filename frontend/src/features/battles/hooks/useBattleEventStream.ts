// One cancellable request loop owns an attempt's initial page, polling, and manual
// refreshes. Status changes resume from the cursor and fetch the terminal tail.
import { useCallback, useEffect, useRef, useState } from 'react'
import { BattleApiError, BattleEvent, BattleRun, listBattleEvents } from '../api/battles'
import { eventPageSize, eventPollIntervalMs } from './constants'
import { type EventState } from '../types'

type EventStream = {
  scope: string; events: BattleEvent[]; cursor: number; hasMore: boolean
  state: EventState; message: string; loaded: boolean
}
function emptyStream(scope: string): EventStream {
  return { scope, events: [], cursor: 0, hasMore: false, state: 'idle', message: '', loaded: false }
}

export function useBattleEventStream(id: string, battleAttempt?: number, battleStatus?: BattleRun['status']) {
  const [selectedAttempt, setSelectedAttempt] = useState<number | null>(null)
  const scope = `${id}:${selectedAttempt}`
  const [stream, setStream] = useState(() => emptyStream(''))
  const snapshot = useRef(stream)
  const request = useRef<(after: number, reset: boolean) => Promise<void>>(async () => {})

  useEffect(() => {
    if (battleAttempt !== undefined) setSelectedAttempt(battleAttempt)
  }, [battleAttempt])

  useEffect(() => {
    if (selectedAttempt === null) return
    const attempt = selectedAttempt
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    let inFlight = false
    if (snapshot.current.scope !== scope) snapshot.current = emptyStream(scope)
    const currentAttempt = attempt === battleAttempt
    const active = battleStatus === 'queued' || battleStatus === 'running'

    function publish(next: EventStream) {
      snapshot.current = next
      setStream(next)
    }
    async function load(after: number, reset: boolean, latest = false) {
      if (controller.signal.aborted || inFlight) return
      inFlight = true
      clearTimeout(timer)
      publish({ ...snapshot.current, state: 'loading', message: '' })
      try {
        const page = await listBattleEvents(id, { attempt, after, limit: eventPageSize, latest }, controller.signal)
        if (controller.signal.aborted) return
        const previous = snapshot.current
        const nextCursor = page.events.at(-1)?.sequence ?? after
        const cursor = reset ? nextCursor : Math.max(previous.cursor, nextCursor)
        const hasMore = !latest && page.events.length === eventPageSize
        publish({ scope, events: mergeEvents(previous.events, page.events, reset), cursor, hasMore, state: 'idle', message: '', loaded: true })
        // Finish draining the current attempt even if it just became terminal.
        if (currentAttempt && (active || hasMore)) {
          timer = setTimeout(() => void load(cursor, false), hasMore ? 0 : eventPollIntervalMs)
        }
      } catch (error) {
        if (controller.signal.aborted) return
        publish({ ...snapshot.current, state: 'error', message: error instanceof BattleApiError ? error.message : 'Battle events could not be loaded.' })
      } finally {
        inFlight = false
      }
    }
    request.current = (after, reset) => {
      const initial = !snapshot.current.loaded
      return load(after, reset || initial, initial)
    }
    const initial = snapshot.current
    void load(initial.cursor, !initial.loaded, !initial.loaded)
    return () => { controller.abort(); clearTimeout(timer) }
  }, [id, selectedAttempt, scope, battleAttempt, battleStatus])

  const loadEventPage = useCallback((after: number, reset: boolean) => request.current(after, reset), [])
  const current = stream.scope === scope ? stream : emptyStream(scope)
  return {
    selectedAttempt, setSelectedAttempt,
    events: current.events, eventCursor: current.cursor, eventHasMore: current.hasMore,
    eventState: current.state, eventMessage: current.message, loadEventPage,
  }
}

/** Deduplicates overlapping pages by sequence and keeps only the latest 500 events in memory. */
function mergeEvents(current: BattleEvent[], incoming: BattleEvent[], reset: boolean) {
  const combined = reset ? incoming : [...current, ...incoming]
  return [...new Map(combined.map((event) => [event.sequence, event])).values()]
    .sort((left, right) => left.sequence - right.sequence)
    .slice(-500)
}
