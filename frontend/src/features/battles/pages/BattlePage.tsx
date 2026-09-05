import { type ReactNode, useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import {
  BattleApiError,
  BattleEvent,
  BattleRun,
  BattleView,
  DecisionHistory,
  JsonValue,
  getBattle,
  getBattleView,
  listBattleEvents,
  listDecisionHistory,
  pauseBattle,
  resumeBattle,
  stopBattle,
} from '../api/battles'
import { Emblem } from '../../../shared/components/Emblem'
import { HexBattleMap } from '../components/HexBattleMap'

const eventPageSize = 50
const eventPollIntervalMs = 1000
type ConnectionState = 'connecting' | 'live' | 'reconnecting' | 'settled' | 'error'
type EventState = 'idle' | 'loading' | 'error'
type BattleControlState = 'idle' | 'pausing' | 'resuming' | 'stopping' | 'error'
type BattleControlAction = 'pause' | 'resume' | 'stop'

const battleControlStates: Record<BattleControlAction, BattleControlState> = {
  pause: 'pausing',
  resume: 'resuming',
  stop: 'stopping',
}

const battleControlRequests = {
  pause: pauseBattle,
  resume: resumeBattle,
  stop: stopBattle,
}

export function BattlePage() {
  const { id = '' } = useParams()
  const { battle, connection, connectionMessage, retryConnection, applyBattle } = useBattleStatus(id)
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
  const [perspective, setPerspective] = useState('spectator')
  const factionView = useFactionPerspective(id, battle, perspective)
  const { decisions, decisionMessage } = useDecisionHistory(id, battle)
  const [sidePanel, setSidePanel] = useState<'events' | 'decisions'>('events')
  const [controlState, setControlState] = useState<BattleControlState>('idle')
  const [controlMessage, setControlMessage] = useState('')

  const progress = battle ? Math.min(100, Math.round((battle.currentTick / battle.requestedTicks) * 100)) : 0
  const participantNames = useMemo(
    () => new Map(battle?.participants.map((participant) => [participant.id, participant.name]) ?? []),
    [battle?.participants],
  )
  const hasModelController = battle?.participants.some((participant) => participant.controller?.type === 'model') ?? false

  const runBattleControl = useCallback(async (action: BattleControlAction) => {
    setControlState(battleControlStates[action])
    setControlMessage('')
    try {
      const updated = await battleControlRequests[action](id)
      applyBattle(updated)
      setControlState('idle')
    } catch (error) {
      setControlState('error')
      setControlMessage(error instanceof BattleApiError ? error.message : 'The battle control could not be applied.')
    }
  }, [applyBattle, id])

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
  const battleTitle = battleTitleFor(battle)
  const mapView = factionView ?? (battle.map && battle.state ? {
    map: battle.map,
    state: battle.state,
    visiblePositions: battle.map.rows.flatMap((row, y) => [...row].map((_, x) => ({ x, y }))),
  } : null)

  return (
    <>
      <BattleHeader battle={battle} battleTitle={battleTitle} connection={connection} connectionMessage={connectionMessage} retryConnection={retryConnection} />

      <section className="battle-overview battle-overview--compact">
        <div className="battle-progress-panel">
          <div className="section-label"><span>COMBAT PROGRESS</span><span>{progress}%</span></div>
          <div className="battle-progress__numbers"><strong>{battle.currentTick}</strong><span>/ {battle.requestedTicks} TICKS</span></div>
          <progress className="battle-progress" aria-label="Battle progress" max={battle.requestedTicks} value={battle.currentTick}>{progress}%</progress>
          <div className="participant-strip">
            {battle.participants.map((participant) => (
              <div key={participant.id}>
                <Emblem seed={participant.name} size="small" />
                <span>{participant.name}<small>{controllerLabel(participant.controller)} · {decisionLabel(latestDecision(decisions, participant.id))}</small></span>
              </div>
            ))}
          </div>
        </div>

        <aside className={`outcome-panel outcome-panel--${battle.paused ? 'paused' : battle.status}`}>
          <span className="outcome-panel__label">BATTLE CONTROL</span>
          <Outcome battle={battle} winnerName={winnerName} />
          <BattleControls battle={battle} state={controlState} message={controlMessage} onControl={runBattleControl} />
        </aside>
      </section>

      <section className={`battle-live-layout ${mapView ? '' : 'battle-live-layout--events-only'}`}>
        <div className="battle-map-column">
          {mapView &&
            <HexBattleMap
              map={mapView.map}
              state={mapView.state}
              visiblePositions={mapView.visiblePositions}
              factionNames={participantNames}
              toolbarControls={<div className="map-perspective-control"><label htmlFor="perspective">Perspective</label><select id="perspective" value={perspective} onChange={(event) => setPerspective(event.target.value)}><option value="spectator">Spectator · full map</option>{battle.participants.map((participant) => <option key={participant.id} value={participant.id}>{participant.name}</option>)}</select></div>}
            />
          }
        </div>
        <aside className="battle-side-panels">
          <div className="battle-side-panel-tabs" role="tablist" aria-label="Battle information">
            <button id="events-tab" role="tab" type="button" aria-selected={sidePanel === 'events'} aria-controls="events-panel" onClick={() => setSidePanel('events')}>EVENTS</button>
            <button id="decisions-tab" role="tab" type="button" aria-selected={sidePanel === 'decisions'} aria-controls="decisions-panel" onClick={() => setSidePanel('decisions')}>AI DECISIONS <span>{decisions.filter((decision) => decision.controllerType === 'model' || Boolean(decision.profileId)).length}</span></button>
          </div>
          <div className="battle-side-panel__content" id={sidePanel === 'events' ? 'events-panel' : 'decisions-panel'} role="tabpanel" aria-labelledby={sidePanel === 'events' ? 'events-tab' : 'decisions-tab'}>
            {sidePanel === 'events'
              ? <EventConsole
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
              : <AIDecisionPanel decisions={decisions} participantNames={participantNames} message={decisionMessage} hasModelController={hasModelController} />}
          </div>
        </aside>
      </section>

      <BattleStatistics battle={battle} participantNames={participantNames} />
    </>
  )
}

function useDecisionHistory(id: string, battle: BattleRun | null) {
  const [decisions, setDecisions] = useState<DecisionHistory[]>([])
  const [message, setMessage] = useState('')
  useEffect(() => {
    if (!battle || battle.attempt < 1) { setDecisions([]); setMessage(''); return }
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    const active = battle.status === 'queued' || battle.status === 'running'
    const attempt = battle.attempt
    async function poll() {
      try {
        setDecisions(await listDecisionHistory(id, attempt, controller.signal))
        setMessage('')
      } catch (error) {
        if (controller.signal.aborted) return
        setMessage(error instanceof BattleApiError ? error.message : 'AI decisions could not be loaded.')
      }
      if (active && !controller.signal.aborted) timer = setTimeout(poll, eventPollIntervalMs)
    }
    void poll()
    return () => { controller.abort(); if (timer) clearTimeout(timer) }
  }, [battle?.attempt, battle?.status, id])
  return { decisions, decisionMessage: message }
}

function latestDecision(decisions: DecisionHistory[], factionId: string) {
  return decisions.findLast((decision) => decision.factionId === factionId)
}

function controllerLabel(controller?: BattleRun['participants'][number]['controller']) {
  if (!controller) return 'Scripted'
  if (controller.type === 'scripted') return 'Scripted'
  return controller.modelIdentifier || controller.profileId || controller.provider || 'Model'
}

function decisionLabel(decision?: DecisionHistory) {
  if (!decision) return 'awaiting window'
  if (decision.status === 'pending') return 'analyzing'
  if (decision.status === 'running') return 'MCP connected'
  if (decision.status === 'accepted') {
    const planName = decision.resolvedPlan?.name ? `: ${decision.resolvedPlan.name}` : ''
    return `plan accepted${planName}`
  }
  if (decision.status === 'fallback') return decision.fallbackSource === 'previous_plan' ? 'previous-plan fallback' : 'scripted fallback'
  const errorCategory = decision.errorCategory ? `: ${decision.errorCategory}` : ''
  return `decision failed${errorCategory}`
}

function AIDecisionPanel({ decisions, participantNames, message, hasModelController }: Readonly<{ decisions: DecisionHistory[]; participantNames: Map<string, string>; message: string; hasModelController: boolean }>) {
  const modelDecisions = decisions.filter((decision) => decision.controllerType === 'model' || Boolean(decision.profileId))
  return (
    <section className="ai-decision-panel" aria-label="AI decisions" aria-live="polite">
      <div className="ai-decision-panel__header">
        <div><span>AI DECISION STREAM</span><h2>Strategy plans</h2></div>
        <strong>{modelDecisions.length}</strong>
      </div>
      {message && <div className="ai-decision-panel__state ai-decision-panel__state--error" role="alert">{message}</div>}
      {!message && modelDecisions.length === 0 && <div className="ai-decision-panel__state">{hasModelController ? 'Waiting for the first production window.' : 'This battle uses scripted controllers, so no model decisions will be created.'}</div>}
      {modelDecisions.length > 0 && (
        <ol className="ai-decision-list">
          {[...modelDecisions].reverse().map((decision) => (
            <li key={decision.requestId}>
              <div className="ai-decision-list__heading">
                <div><strong>{participantNames.get(decision.factionId) ?? decision.factionId}</strong><span>TICK {decision.targetTick}</span></div>
                <span className={`decision-state decision-state--${decision.status}`}>{decisionLabel(decision)}</span>
              </div>
              <dl>
                <DecisionDetail label="Plan" value={decision.resolvedPlan?.name ?? (decision.status === 'running' || decision.status === 'pending' ? 'Analyzing…' : 'No plan submitted')} />
                <DecisionDetail label="Rationale" value={decision.resolvedPlan?.rationale ?? '—'} />
                <DecisionDetail label="Tactics" value={formatWeights(decision.resolvedPlan?.actionWeights)} />
                <DecisionDetail label="Reserve" value={decision.resolvedPlan ? `${decision.resolvedPlan.reserveGold ?? 0} gold` : '—'} />
                <DecisionDetail label="Spend order" value={formatList(decision.resolvedPlan?.spendOrder)} />
                <DecisionDetail label="Recruit" value={formatList(decision.resolvedPlan?.recruitmentPriorities)} />
                <DecisionDetail label="Build sites" value={formatCoordinates(decision.resolvedPlan?.constructionPriorities)} />
                <DecisionDetail label="Model" value={decision.modelIdentifier || decision.profileId || '—'} />
              </dl>
            </li>
          ))}
        </ol>
      )}
    </section>
  )
}

function DecisionDetail({ label, value }: Readonly<{ label: string; value: string }>) {
  return <div><dt>{label}</dt><dd>{value}</dd></div>
}

function formatWeights(weights?: Record<string, number>) {
  if (!weights || Object.keys(weights).length === 0) return '—'
  return Object.entries(weights).map(([name, weight]) => `${name} ${weight}`).join(' · ')
}

function formatList(values?: string[]) {
  return values?.length ? values.join(' → ') : '—'
}

function formatCoordinates(values?: { x: number; y: number }[]) {
  return values?.length ? values.map(({ x, y }) => `(${x},${y})`).join(' → ') : '—'
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
              <Stat label="Castle health" value={stats.castleHealth ?? 0} />
              <Stat label="Recruited / lost" value={`${stats.unitsRecruited ?? 0} / ${stats.unitsLost ?? 0}`} />
              <Stat label="Damage dealt" value={stats.damageDealt} />
              <Stat label="Damage taken" value={stats.damageTaken} />
              <Stat label="Attack ticks" value={stats.attackTicks} />
              <Stat label="Defend ticks" value={stats.defendTicks} />
              <Stat label="Gold earned / spent" value={`${stats.goldEarned ?? 0} / ${stats.goldSpent ?? 0}`} />
              <Stat label="Loot collected" value={stats.goldLooted ?? 0} />
              <Stat label="Buildings built / destroyed" value={`${stats.buildingsConstructed ?? 0} / ${stats.buildingsDestroyed ?? 0}`} />
            </dl>
          </article>
        ))}
      </div>
    </section>
  )
}

function useFactionPerspective(id: string, battle: BattleRun | null, perspective: string) {
  const [view, setView] = useState<BattleView | null>(null)
  useEffect(() => {
    if (!battle || perspective === 'spectator') { setView(null); return }
    const controller = new AbortController()
    getBattleView(id, perspective, controller.signal).then(setView).catch(() => setView(null))
    return () => controller.abort()
  }, [battle?.currentTick, id, perspective])
  return view
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
  const applyBattle = useCallback((current: BattleRun) => {
    setBattle(current)
    setConnection(current.status === 'queued' || current.status === 'running' ? 'live' : 'settled')
    setConnectionMessage('')
  }, [])
  return { battle, connection, connectionMessage, retryConnection, applyBattle }
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

function battleTitleFor(battle: BattleRun) {
  if (battle.status === 'completed') return 'Battle complete'
  if (battle.status === 'failed') return 'Battle failed'
  if (battle.status === 'stopped') return 'Battle stopped'
  if (battle.paused) return 'Battle paused'
  return 'Battle in progress'
}

function Outcome({ battle, winnerName }: Readonly<{ battle: BattleRun; winnerName: string | null }>) {
  if (battle.status === 'completed' && battle.result) {
    return (
      <>
        <strong>{winnerName ? `${winnerName} wins` : 'Draw'}</strong>
        <p>{battle.result.reason === 'castle_destroyed' ? 'The enemy castle was destroyed.' : 'Maximum tick limit reached.'}</p>
      </>
    )
  }
  if (battle.status === 'failed') {
    return <><strong>Engine halted</strong><p>{battle.errorMessage ?? battle.errorCode ?? 'The battle could not be completed.'}</p></>
  }
  if (battle.status === 'stopped') {
    return <><strong>Stopped by operator</strong><p>The engine and unresolved AI decisions have been halted.</p></>
  }
  if (battle.status === 'queued') {
    if (battle.paused) return <><strong>Deployment paused</strong><p>Press Play when the engine may claim this battle.</p></>
    return <><strong>Awaiting engine</strong><p>A worker will claim this battle shortly.</p></>
  }
  if (battle.paused) {
    return <><strong>Battle paused</strong><p>The engine is holding its lease and will continue from this tick.</p></>
  }
  return <><strong>Forces engaged</strong><p>Status refreshes automatically every second.</p></>
}

function BattleControls({ battle, state, message, onControl }: Readonly<{
  battle: BattleRun
  state: BattleControlState
  message: string
  onControl: (action: BattleControlAction) => Promise<void>
}>) {
  const active = battle.status === 'queued' || battle.status === 'running'
  const busy = state === 'pausing' || state === 'resuming' || state === 'stopping'
  return (
    <div className="battle-controls">
      <fieldset aria-label="Battle controls">
        <button type="button" aria-pressed={active && !battle.paused} disabled={!active || !battle.paused || busy} onClick={() => void onControl('resume')}>{state === 'resuming' ? 'RESUMING…' : 'PLAY'}</button>
        <button type="button" aria-pressed={active && battle.paused} disabled={!active || battle.paused || busy} onClick={() => void onControl('pause')}>{state === 'pausing' ? 'PAUSING…' : 'PAUSE'}</button>
        <button className="battle-control--stop" type="button" disabled={!active || busy} onClick={() => void onControl('stop')}>{state === 'stopping' ? 'STOPPING…' : 'STOP'}</button>
      </fieldset>
      {message && <output role="alert">{message}</output>}
    </div>
  )
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

function formatEventValue(value: JsonValue) {
  if (value === null) return '—'
  if (typeof value === 'object') return JSON.stringify(value)
  if (typeof value === 'boolean') return value ? 'true' : 'false'
  if (typeof value === 'number') return value.toString()
  return value
}
