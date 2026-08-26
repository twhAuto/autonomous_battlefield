import { type ReactNode, useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import {
  BattleApiError,
  BattleEvent,
  BattleRun,
  getBattle,
  listBattleEvents,
} from '../api/battles'
import { Emblem } from '../components/Emblem'

const eventPageSize = 50
type ConnectionState = 'connecting' | 'live' | 'reconnecting' | 'settled' | 'error'
type EventState = 'idle' | 'loading' | 'error'

export function BattlePage() {
  const { id = '' } = useParams()
  const { battle, connection, connectionMessage, retryConnection } = useBattleStatus(id)
  const {
    selectedAttempt,
    setSelectedAttempt,
    events,
    eventCursor,
    eventHasMore,
    eventState,
    eventMessage,
    loadEventPage,
  } = useBattleEventStream(id, battle?.attempt, battle?.status)

  const progress = battle ? Math.min(100, Math.round((battle.currentTick / battle.requestedTicks) * 100)) : 0
  const participantNames = useMemo(
    () => new Map(battle?.participants.map((participant) => [participant.id, participant.name]) ?? []),
    [battle?.participants],
  )

  if (!battle && connection === 'connecting') {
    return <BattleState code="ESTABLISHING LINK" title="Locating battle" message="Connecting to the battle engine state…" loading />
  }
  if (!battle && (connection === 'reconnecting' || connection === 'error')) {
    return (
      <BattleState code="BATTLE LINK INTERRUPTED" title="Battle unavailable" message={connectionMessage}>
        {connection === 'error' && <button className="compact-action" type="button" onClick={retryConnection}>RETRY CONNECTION</button>}
      </BattleState>
    )
  }
  if (!battle) return null

  const winnerName = battle.result?.winnerFactionId
    ? participantNames.get(battle.result.winnerFactionId) ?? battle.result.winnerFactionId
    : null
  const battleTitle = battleTitleFor(battle.status)

  return (
    <>
      <BattleHeader battle={battle} battleTitle={battleTitle} connection={connection} connectionMessage={connectionMessage} retryConnection={retryConnection} />

      <section className="battle-overview">
        <div className="battle-progress-panel">
          <div className="section-label"><span>COMBAT PROGRESS</span><span>{progress}%</span></div>
          <div className="battle-progress__numbers"><strong>{battle.currentTick}</strong><span>/ {battle.requestedTicks} TICKS</span></div>
          <progress className="battle-progress" aria-label="Battle progress" max={battle.requestedTicks} value={battle.currentTick}>{progress}%</progress>
          <div className="participant-strip">
            {battle.participants.map((participant) => (
              <div key={participant.id}>
                <Emblem seed={participant.name} size="small" />
                <span>{participant.name}</span>
              </div>
            ))}
          </div>
        </div>

        <aside className={`outcome-panel outcome-panel--${battle.status}`}>
          <span className="outcome-panel__label">ENGAGEMENT OUTCOME</span>
          <Outcome battle={battle} winnerName={winnerName} />
        </aside>
      </section>

      <BattleStatistics battle={battle} participantNames={participantNames} />
      <EventConsole
        battleAttempt={battle.attempt}
        selectedAttempt={selectedAttempt}
        setSelectedAttempt={setSelectedAttempt}
        events={events}
        eventCursor={eventCursor}
        eventHasMore={eventHasMore}
        eventState={eventState}
        eventMessage={eventMessage}
        loadEventPage={loadEventPage}
      />
    </>
  )
}

function BattleHeader({ battle, battleTitle, connection, connectionMessage, retryConnection }: Readonly<{
  battle: BattleRun
  battleTitle: string
  connection: ConnectionState
  connectionMessage: string
  retryConnection: () => void
}>) {
  return (
    <section className="page-header battle-status-header">
      <Link className="back-link" to="/battles/new">← NEW BATTLE</Link>
      <div className="battle-status-heading">
        <div>
          <div className="hero__eyebrow"><span>04</span> LIVE ENGAGEMENT</div>
          <h1>{battleTitle}</h1>
        </div>
        <ConnectionBadge state={connection} />
      </div>
      {connectionMessage && (
        <output className="connection-message">
          <span>{connectionMessage}</span>
          {connection === 'error' && <button className="text-button" type="button" onClick={retryConnection}>RETRY CONNECTION</button>}
        </output>
      )}
      <div className="battle-meta-row">
        <span>SCENARIO <strong>{battle.scenarioId}</strong></span>
        <span>ATTEMPT <strong>{battle.attempt}</strong></span>
        <span>STATUS <strong>{battle.status}</strong></span>
        <code title={battle.id}>{battle.id}</code>
      </div>
    </section>
  )
}

function BattleStatistics({ battle, participantNames }: Readonly<{ battle: BattleRun; participantNames: Map<string, string> }>) {
  if (!battle.result) return null
  return (
    <section className="battle-statistics">
      <div className="section-label"><span>AFTER-ACTION STATISTICS</span><span>{battle.result.ticksRun} TICKS</span></div>
      <div className="statistics-grid">
        {battle.result.factions.map((stats) => (
          <article key={stats.factionId} className="statistics-card">
            <h2>{participantNames.get(stats.factionId) ?? stats.factionId}</h2>
            <dl>
              <Stat label="Units" value={`${stats.unitsRemaining} / ${stats.unitsStarted}`} />
              <Stat label="Damage dealt" value={stats.damageDealt} />
              <Stat label="Damage taken" value={stats.damageTaken} />
              <Stat label="Attack ticks" value={stats.attackTicks} />
              <Stat label="Defend ticks" value={stats.defendTicks} />
            </dl>
          </article>
        ))}
      </div>
    </section>
  )
}

function EventConsole({ battleAttempt, selectedAttempt, setSelectedAttempt, events, eventCursor, eventHasMore, eventState, eventMessage, loadEventPage }: Readonly<{
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
  return (
    <section className="event-console" aria-live="polite">
      <div className="event-console__header">
        <div>
          <span>BATTLE EVENT STREAM</span>
          <h2>Attempt history</h2>
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
          {events.map((event) => <EventRow key={event.sequence} event={event} />)}
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

function useBattleStatus(id: string) {
  const [battle, setBattle] = useState<BattleRun | null>(null)
  const [connection, setConnection] = useState<ConnectionState>('connecting')
  const [connectionMessage, setConnectionMessage] = useState('')
  const [retryVersion, setRetryVersion] = useState(0)

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
    return () => {
      stopped = true
      controller.abort()
      if (timer) clearTimeout(timer)
    }
  }, [id, retryVersion])

  const retryConnection = useCallback(() => setRetryVersion((value) => value + 1), [])
  return { battle, connection, connectionMessage, retryConnection }
}

function useBattleEventStream(id: string, battleAttempt?: number, battleStatus?: BattleRun['status']) {
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

  const loadEventPage = useCallback(async (after: number, reset: boolean, signal?: AbortSignal) => {
    if (selectedAttempt === null) return
    setEventState('loading')
    setEventMessage('')
    try {
      const page = await listBattleEvents(id, { attempt: selectedAttempt, after, limit: eventPageSize }, signal)
      setEvents((current) => mergeEvents(current, page.events, reset))
      const nextCursor = page.events.at(-1)?.sequence ?? after
      setEventCursor((current) => Math.max(current, nextCursor))
      setEventHasMore(page.events.length === eventPageSize)
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
    void loadEventPage(0, true, controller.signal)
    return () => controller.abort()
  }, [loadEventPage, selectedAttempt])

  useEffect(() => {
    const isCurrentAttempt = selectedAttempt === battleAttempt
    const isActive = battleStatus === 'queued' || battleStatus === 'running'
    if (!isCurrentAttempt || !isActive || eventHasMore) return
    const controller = new AbortController()
    const timer = setTimeout(() => void loadEventPage(eventCursor, false, controller.signal), 2000)
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

function battleTitleFor(status: BattleRun['status']) {
  if (status === 'completed') return 'Battle complete'
  if (status === 'failed') return 'Battle failed'
  return 'Battle in progress'
}

function Outcome({ battle, winnerName }: Readonly<{ battle: BattleRun; winnerName: string | null }>) {
  if (battle.status === 'completed' && battle.result) {
    return (
      <>
        <strong>{winnerName ? `${winnerName} wins` : 'Draw'}</strong>
        <p>{battle.result.reason === 'elimination' ? 'Opposing forces eliminated.' : 'Maximum tick limit reached.'}</p>
      </>
    )
  }
  if (battle.status === 'failed') {
    return <><strong>Engine halted</strong><p>{battle.errorMessage ?? battle.errorCode ?? 'The battle could not be completed.'}</p></>
  }
  if (battle.status === 'queued') {
    return <><strong>Awaiting engine</strong><p>A worker will claim this battle shortly.</p></>
  }
  return <><strong>Forces engaged</strong><p>Status refreshes automatically every second.</p></>
}

function ConnectionBadge({ state }: Readonly<{ state: string }>) {
  return <div className={`connection-badge connection-badge--${state}`}><span className="status-dot" />{state.toUpperCase()}</div>
}

function BattleState({ code, title, message, loading, children }: Readonly<{ code: string; title: string; message: string; loading?: boolean; children?: ReactNode }>) {
  return (
    <section className="directory-state directory-state--battle" role={loading ? undefined : 'alert'}>
      {loading && <span className="loader" aria-hidden="true" />}
      <span className="directory-state__code">{code}</span>
      <h2>{title}</h2>
      <p>{message}</p>
      {children}
    </section>
  )
}

function Stat({ label, value }: Readonly<{ label: string; value: string | number }>) {
  return <div><dt>{label}</dt><dd>{value}</dd></div>
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

function formatEventValue(value: unknown) {
  if (value === null) return '—'
  if (typeof value === 'object') return JSON.stringify(value) ?? '—'
  return value === undefined ? 'undefined' : value.toString()
}
