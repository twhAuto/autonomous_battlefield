// Renders an attempt-selectable battle event log with paging controls and readable event
// payload values.

import { useState } from 'react'
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
  entities?: EntityNaming['entities']
  eventCursor: number
  eventHasMore: boolean
  eventState: EventState
  eventMessage: string
  loadEventPage: (after: number, reset: boolean, signal?: AbortSignal) => Promise<void>
}>) {
  const [eventType, setEventType] = useState('all')
  const eventTypes = [...new Set([...events.map((event) => event.type), ...(eventType === 'all' ? [] : [eventType])])].sort()
  const visibleEvents = events.filter((event) => eventType === 'all' || event.type === eventType)
  const latestTick = events.reduce((latest, event) => Math.max(latest, event.tick), 0)
  return (
    <section className="event-console" aria-live="polite">
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
      <label className="panel-filter" htmlFor="event-type">Event type<select id="event-type" value={eventType} onChange={(event) => setEventType(event.target.value)}><option value="all">All events</option>{eventTypes.map((type) => <option value={type} key={type}>{type.replaceAll('_', ' ')}</option>)}</select></label>

      {eventState === 'error' && <div className="event-console__error" role="alert">{eventMessage} <button type="button" onClick={() => void loadEventPage(eventCursor, false)}>Retry</button></div>}
      {events.length === 0 && eventState !== 'error' && (
        <div className="event-console__empty">{eventState === 'loading' ? 'Loading event stream…' : 'No events recorded for this attempt yet.'}</div>
      )}
      {events.length > 0 && (
        <ol className="event-list">
          {[...visibleEvents].reverse().map((event) => <EventRow key={`${event.attempt}-${event.sequence}`} event={event} naming={{ factionNames: participantNames, entities: event.attempt === battleAttempt ? entities : undefined }} />)}
        </ol>
      )}
      {events.length > 0 && visibleEvents.length === 0 && <div className="event-console__empty">No matching events in the loaded history.</div>}
      <div className="event-console__actions">
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
        <dl>
          {Object.entries(event.data).map(([key, value]) => (
            <div key={key}><dt>{key}</dt><dd title={formatEventValue(value)}>{typeof value === 'string' ? eventStringLabel(key, value, naming) : formatEventValue(value)}</dd></div>
          ))}
        </dl>
        <details className="event-payload"><summary>Inspect event #{event.sequence}</summary><pre>{JSON.stringify(event.data, null, 2)}</pre></details>
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
