// Renders an attempt-selectable battle event log with paging controls and readable event
// payload values.

import { useEffect, useState } from 'react'
import { useMobile } from '../../../shared/hooks/useMobile'
import { eventSummary, matchesPreset, type EventPreset } from '../presentation/events'
import { BattleEvent, JsonValue } from '../api/battles'
import { eventPageSize } from '../hooks/constants'
import { eventStringLabel, type EntityNaming } from '../presentation/entities'
import { type EventState } from '../types'

export function EventConsole({ battleAttempt, selectedAttempt, setSelectedAttempt, events, eventCursor, eventHasMore, eventState, eventMessage, loadEventPage, participantNames = new Map(), entities }: Readonly<{
  battleAttempt: number
  selectedAttempt: number | null
  setSelectedAttempt: (attempt: number) => void
  events: BattleEvent[]
  participantNames?: Map<string, string>
  entities?: NonNullable<EntityNaming['entities']>
  eventCursor: number
  eventHasMore: boolean
  eventState: EventState
  eventMessage: string
  loadEventPage: (after: number, reset: boolean, signal?: AbortSignal) => Promise<void>
}>) {
  const [eventType, setEventType] = useState('all')
  const [preset, setPreset] = useState<EventPreset>('key')
  const [limit, setLimit] = useState(10)
  const mobile = useMobile()
  useEffect(() => { setLimit(10) }, [eventType, preset, selectedAttempt])
  const eventTypes = [...new Set([...events.map((event) => event.type), ...(eventType === 'all' ? [] : [eventType])])].sort((a, b) => a.localeCompare(b))
  const matchingEvents = events.filter((event) => matchesPreset(event, preset) && (eventType === 'all' || event.type === eventType))
  const visibleEvents = [...matchingEvents].reverse().slice(0, mobile ? limit : undefined)
  const latestTick = events.reduce((latest, event) => Math.max(latest, event.tick), 0)
  return (
    <section className="event-console">
      <output className="visually-hidden">{events.length} events loaded for this attempt.</output>
      <div className="event-console__header">
        <div>
          <span>BATTLE EVENT STREAM{latestTick > 0 ? ` · LATEST TICK ${latestTick}` : ''}</span>
          <h2>Latest events</h2>
        </div>
        <label htmlFor="attempt-select"><span>Attempt</span>
          <select id="attempt-select" value={selectedAttempt ?? 0} onChange={(event) => setSelectedAttempt(Number(event.target.value))}>
            {battleAttempt === 0 && <option value={0}>Queued</option>}
            {Array.from({ length: battleAttempt }, (_, index) => index + 1).map((attempt) => <option key={attempt} value={attempt}>Attempt {attempt}</option>)}
          </select>
        </label>
      </div>
      <label className="panel-filter" htmlFor="event-preset">Event group<select id="event-preset" value={preset} onChange={event => { setPreset(event.target.value as EventPreset); setEventType('all') }}><option value="key">Key events</option><option value="combat">Combat</option><option value="economy">Economy</option><option value="all">All events</option></select></label>
      <label className="panel-filter" htmlFor="event-type">Event type<select id="event-type" value={eventType} onChange={(event) => { setEventType(event.target.value); if (event.target.value !== 'all') setPreset('all') }}><option value="all">All types</option>{eventTypes.map((type) => <option value={type} key={type}>{type.replaceAll('_', ' ')}</option>)}</select></label>
      <p className="field-hint">Filters apply to loaded history.</p>

      {eventState === 'error' && <div className="event-console__error" role="alert">{eventMessage} <button type="button" onClick={() => void loadEventPage(eventCursor, false)}>Retry</button></div>}
      {events.length === 0 && eventState !== 'error' && (
        <div className="event-console__empty">{eventState === 'loading' ? 'Loading event stream…' : 'No events recorded for this attempt yet.'}</div>
      )}
      {events.length > 0 && (
        <ol className="event-list">
          {visibleEvents.map((event) => <EventRow key={`${event.attempt}-${event.sequence}`} event={event} naming={{ factionNames: participantNames, entities: event.attempt === battleAttempt ? entities : undefined }} />)}
        </ol>
      )}
      {events.length > 0 && visibleEvents.length === 0 && <div className="event-console__empty">No matching events in the loaded history.</div>}
      <div className="event-console__actions">
        {mobile && matchingEvents.length > limit && <button type="button" className="compact-action" onClick={() => setLimit(value => value + 10)}>Show more events</button>}
        {eventHasMore && <button className="compact-action" type="button" disabled={eventState === 'loading'} onClick={() => void loadEventPage(eventCursor, false)}>LOAD NEXT {eventPageSize}</button>}
        {!eventHasMore && events.length > 0 && <button className="text-button" type="button" disabled={eventState === 'loading'} onClick={() => void loadEventPage(eventCursor, false)}>REFRESH EVENTS</button>}
        <span>SHOWING {visibleEvents.length} OF {events.length} LOADED EVENTS</span>
      </div>
    </section>
  )
}

function EventRow({ event, naming }: Readonly<{ event: BattleEvent; naming: EntityNaming }>) {
  return (
    <li>
      <div className="event-list__index">#{event.sequence.toString().padStart(4, '0')}</div>
      <div className="event-list__body">
        <div><strong>{event.type.replaceAll('_', ' ')}</strong><span>TICK {event.tick}</span></div>
        <p>{eventSummary(event, naming)}</p>
        <details className="event-payload"><summary>Details · Inspect event #{event.sequence}</summary>
        <dl>
          {Object.entries(event.data).map(([key, value]) => (
            <div key={key}><dt>{key}</dt><dd title={formatEventValue(value)}>{typeof value === 'string' ? eventStringLabel(key, value, naming) : formatEventValue(value)}</dd></div>
          ))}
        </dl>
        <pre>{JSON.stringify(event.data, null, 2)}</pre></details>
      </div>
    </li>
  )
}

function formatEventValue(value: JsonValue) {
  if (value === null) return '—'
  if (typeof value === 'object') return JSON.stringify(value)
  if (typeof value === 'boolean') return value ? 'true' : 'false'
  if (typeof value === 'number') return value.toString()
  return value
}
