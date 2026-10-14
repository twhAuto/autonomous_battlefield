// Composes battle polling hooks, perspective selection, map and decision panels, event
// history, and pause/resume/stop controls.

import { useCallback, useMemo, useState } from 'react'
import { usePermissions } from '../../auth/SessionContext'
import { useParams } from 'react-router-dom'
import { BattleApiError, pauseBattle, resumeBattle, stopBattle } from '../api/battles'
import { HexBattleMap } from '../components/HexBattleMap'
import { type BattleControlState, type BattleControlAction } from '../types'
import { useDecisionHistory } from '../hooks/useDecisionHistory'
import { BattleInspector } from '../components/BattleInspector'
import { FactionSummaries } from '../components/FactionSummaries'
import { BattleHeader, BattleStatistics, battleTitleFor, Outcome, BattleControls, BattleState } from '../components/BattlePresentation'
import { useFactionPerspective } from '../hooks/useFactionPerspective'
import { useBattleStatus } from '../hooks/useBattleStatus'
import { useBattleEventStream } from '../hooks/useBattleEventStream'

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
  return <BattleDetail key={id} id={id} />
}

function BattleDetail({ id }: Readonly<{ id: string }>) {
  const permissions = usePermissions()
  const { battle, connection, connectionMessage, lastUpdated, retryConnection, applyBattle } = useBattleStatus(id)
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
  const { view: factionView, message: perspectiveMessage, retry: retryPerspective } = useFactionPerspective(id, battle, perspective)
  const { decisions, decisionMessage } = useDecisionHistory(id, battle, permissions.inspect)
  const [controlState, setControlState] = useState<BattleControlState>('idle')
  const [controlMessage, setControlMessage] = useState('')

  const progress = battle ? Math.min(100, Math.round((battle.currentTick / battle.requestedTicks) * 100)) : 0
  const participantNames = useMemo(
    () => new Map(battle?.participants.map((participant) => [participant.id, participant.name]) ?? []),
    [battle?.participants],
  )

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
  const spectatorView = battle.map && battle.state ? {
    map: battle.map,
    state: battle.state,
    visiblePositions: battle.map.rows.flatMap((row, y) => [...row].map((_, x) => ({ x, y }))),
  } : null
  // Keep the map shell mounted (including its modal and camera), but replace all
  // map content with feedback until the selected perspective is available.
  const mapView = perspective === 'spectator' ? spectatorView : factionView
  const displayView = mapView ?? spectatorView
  const perspectiveFeedback = perspectiveMessage ? <p role="alert">{perspectiveMessage} <button className="text-button" type="button" onClick={retryPerspective}>Retry faction view</button></p> : <p role="status">Loading faction view…</p>
  const perspectiveControl = <div className="map-perspective-control"><label htmlFor="perspective">Perspective</label><select id="perspective" value={perspective} onChange={(event) => setPerspective(event.target.value)}><option value="spectator">Spectator · full map</option>{battle.participants.map((participant) => <option key={participant.id} value={participant.id}>{participant.name}</option>)}</select></div>

  return (
    <>
      <BattleHeader battle={battle} battleTitle={battleTitle} connection={connection} connectionMessage={connectionMessage} lastUpdated={lastUpdated} retryConnection={retryConnection} />

      <section className="battle-toolbar">
        <div className="battle-progress-panel">
          <div className="section-label"><span>Tick limit used</span></div>
          <p>Tick <strong>{battle.currentTick}</strong> of {battle.requestedTicks} maximum</p>
          <progress className="battle-progress" aria-label="Tick limit used" max={battle.requestedTicks} value={battle.currentTick}>{progress}%</progress>
        </div>

        <FactionSummaries battle={battle} decisions={decisions} decisionMessage={decisionMessage} />

        <aside className={`outcome-panel outcome-panel--${battle.paused ? 'paused' : battle.status}`}>
          <span className="outcome-panel__label">{['completed', 'stopped', 'failed'].includes(battle.status) ? 'Battle result' : 'Battle control'}</span>
          <Outcome battle={battle} winnerName={winnerName} />
          {permissions.canManage(battle.ownerId) && <BattleControls battle={battle} state={controlState} message={controlMessage} onControl={runBattleControl} />}
        </aside>
      </section>

      <section className={`battle-live-layout ${displayView ? '' : 'battle-live-layout--events-only'}`}>
        <div className="battle-map-column">
          {displayView &&
            <HexBattleMap
              map={displayView.map}
              state={displayView.state}
              visiblePositions={displayView.visiblePositions}
              factionNames={participantNames}
              toolbarControls={perspectiveControl}
              placeholder={!mapView ? perspectiveFeedback : undefined}
            />
          }
        </div>
        {permissions.inspect && <BattleInspector key={`${id}:${battle.attempt}`} battle={battle} decisions={decisions} decisionMessage={decisionMessage} events={{
          battleAttempt: battle.attempt, selectedAttempt, setSelectedAttempt, events, participantNames,
          eventCursor, eventHasMore, eventState, eventMessage, loadEventPage,
        }} />}
      </section>

      <BattleStatistics battle={battle} participantNames={participantNames} />
    </>
  )
}
