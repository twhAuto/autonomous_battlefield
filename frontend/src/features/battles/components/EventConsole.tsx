import { BattleEvent, JsonValue } from '../api/battles'
import { eventPageSize } from '../hooks/constants'
import { type EventState } from '../types'

export function EventConsole({ battleAttempt, selectedAttempt, setSelectedAttempt, events, eventCursor, eventHasMore, eventState, eventMessage, loadEventPage }: Readonly<{
  battleAttempt: number
  selectedAttempt: number | null
  setSelectedAttempt: (attempt: number) => void
  events: BattleEvent[]
  eventCursor: number
  eventHasMore: boolean
  eventState: EventState
  eventMessage: string
  loadEventPage: (after: number, reset: boolean, signal?: AbortSignal) => Promise<void>
}>) {
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

      {eventState === 'error' && <div className="event-console__error" role="alert">{eventMessage} <button type="button" onClick={() => void loadEventPage(eventCursor, false)}>Retry</button></div>}
      {events.length === 0 && eventState !== 'error' && (
        <div className="event-console__empty">{eventState === 'loading' ? 'Loading event stream…' : 'No events recorded for this attempt yet.'}</div>
      )}
      {events.length > 0 && (
        <ol className="event-list">
          {[...events].reverse().map((event) => <EventRow key={event.sequence} event={event} />)}
        </ol>
      )}
      <div className="event-console__actions">
        {eventHasMore && <button className="compact-action" type="button" disabled={eventState === 'loading'} onClick={() => void loadEventPage(eventCursor, false)}>LOAD NEXT {eventPageSize}</button>}
        {!eventHasMore && events.length > 0 && <button className="text-button" type="button" disabled={eventState === 'loading'} onClick={() => void loadEventPage(eventCursor, false)}>REFRESH EVENTS</button>}
        <span>SHOWING THE LATEST {events.length} EVENTS</span>
      </div>
    </section>
  )
}

function EventRow({ event }: Readonly<{ event: BattleEvent }>) {
  return (
    <li>
      <div className="event-list__index">#{event.sequence.toString().padStart(4, '0')}</div>
      <div className="event-list__body">
        <div><strong>{event.type.replaceAll('_', ' ')}</strong><span>TICK {event.tick}</span></div>
        <dl>
          {Object.entries(event.data).map(([key, value]) => (
            <div key={key}><dt>{key}</dt><dd>{formatEventValue(value)}</dd></div>
          ))}
        </dl>
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
