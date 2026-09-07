// Composes battle polling hooks, perspective selection, map and decision panels, event
// history, and pause/resume/stop controls.

import { useCallback, useMemo, useState } from 'react'
import { useParams } from 'react-router-dom'
import { BattleApiError, pauseBattle, resumeBattle, stopBattle } from '../api/battles'
import { HexBattleMap } from '../components/HexBattleMap'
import { type BattleControlState, type BattleControlAction } from '../types'
import { useDecisionHistory } from '../hooks/useDecisionHistory'
import { BattleInspector } from '../components/BattleInspector'
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
  const mapView = factionView ?? (battle.map && battle.state ? {
    map: battle.map,
    state: battle.state,
    visiblePositions: battle.map.rows.flatMap((row, y) => [...row].map((_, x) => ({ x, y }))),
  } : null)

  return (
    <>
      <BattleHeader battle={battle} battleTitle={battleTitle} connection={connection} connectionMessage={connectionMessage} retryConnection={retryConnection} />

      <section className="battle-toolbar">
        <div className="battle-progress-panel">
          <div className="section-label"><span>COMBAT PROGRESS</span><span>{progress}%</span></div>
          <div className="battle-progress__numbers"><strong>{battle.currentTick}</strong><span>/ {battle.requestedTicks} TICKS</span></div>
          <progress className="battle-progress" aria-label="Battle progress" max={battle.requestedTicks} value={battle.currentTick}>{progress}%</progress>
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
        <BattleInspector key={`${id}:${battle.attempt}`} battle={battle} decisions={decisions} decisionMessage={decisionMessage} events={{
          battleAttempt: battle.attempt, selectedAttempt, setSelectedAttempt, events, participantNames,
          eventCursor, eventHasMore, eventState, eventMessage, loadEventPage,
        }} />
      </section>

      <BattleStatistics battle={battle} participantNames={participantNames} />
    </>
  )
}
