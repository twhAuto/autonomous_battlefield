import { useCallback, useEffect, useState } from 'react'
import { BattleApiError, BattleEvent, BattleRun, listBattleEvents } from '../api/battles'
import { eventPageSize, eventPollIntervalMs } from './constants'
import { type EventState } from '../types'

export function useBattleEventStream(id: string, battleAttempt?: number, battleStatus?: BattleRun['status']) {
  const [selectedAttempt, setSelectedAttempt] = useState<number | null>(null)
  const [events, setEvents] = useState<BattleEvent[]>([])
  const [eventCursor, setEventCursor] = useState(0)
  const [eventHasMore, setEventHasMore] = useState(false)
  const [eventState, setEventState] = useState<EventState>('idle')
  const [eventMessage, setEventMessage] = useState('')
  const [eventPollVersion, setEventPollVersion] = useState(0)

  useEffect(() => {
    if (battleAttempt === undefined) return
    setSelectedAttempt(battleAttempt)
  }, [battleAttempt])

  const loadEventPage = useCallback(async (after: number, reset: boolean, signal?: AbortSignal, latest = false) => {
    if (selectedAttempt === null) return
    setEventState('loading')
    setEventMessage('')
    try {
      const page = await listBattleEvents(id, { attempt: selectedAttempt, after, limit: eventPageSize, latest }, signal)
      setEvents((current) => mergeEvents(current, page.events, reset))
      const nextCursor = page.events.at(-1)?.sequence ?? after
      setEventCursor((current) => Math.max(current, nextCursor))
      setEventHasMore(!latest && page.events.length === eventPageSize)
      setEventState('idle')
      setEventPollVersion((current) => current + 1)
    } catch (error) {
      if (signal?.aborted) return
      setEventState('error')
      setEventMessage(error instanceof BattleApiError ? error.message : 'Battle events could not be loaded.')
    }
  }, [id, selectedAttempt])

  useEffect(() => {
    if (selectedAttempt === null) return
    const controller = new AbortController()
    setEvents([])
    setEventCursor(0)
    setEventHasMore(false)
    void loadEventPage(0, true, controller.signal, true)
    return () => controller.abort()
  }, [loadEventPage, selectedAttempt])

  useEffect(() => {
    const isCurrentAttempt = selectedAttempt === battleAttempt
    const isActive = battleStatus === 'queued' || battleStatus === 'running'
    if (!isCurrentAttempt || !isActive) return
    const controller = new AbortController()
    // A busy tick can produce more than one page of events. Drain full pages
    // immediately, then return to the normal polling cadence once caught up.
    const delay = eventHasMore ? 0 : eventPollIntervalMs
    const timer = setTimeout(() => void loadEventPage(eventCursor, false, controller.signal), delay)
    return () => {
      controller.abort()
      clearTimeout(timer)
    }
  }, [battleAttempt, battleStatus, eventCursor, eventHasMore, eventPollVersion, loadEventPage, selectedAttempt])

  return {
    selectedAttempt,
    setSelectedAttempt,
    events,
    eventCursor,
    eventHasMore,
    eventState,
    eventMessage,
    loadEventPage,
  }
}

function mergeEvents(current: BattleEvent[], incoming: BattleEvent[], reset: boolean) {
  const combined = reset ? incoming : [...current, ...incoming]
  return [...new Map(combined.map((event) => [event.sequence, event])).values()]
    .sort((left, right) => left.sequence - right.sequence)
    .slice(-500)
}
