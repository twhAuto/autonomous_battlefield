// Composes battle polling hooks, map and decision panels, event
// history, pause/resume/stop controls, after-action results over the map, and replay
// mode for settled battles.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { usePermissions } from '../../auth/SessionContext'
import { useParams } from 'react-router-dom'
import { BattleApiError, pauseBattle, resumeBattle, stopBattle, type BattleMap, type BattleRun, type DecisionHistory } from '../api/battles'
import { HexBattleMap } from '../components/HexBattleMap'
import { type BattleControlState, type BattleControlAction } from '../types'
import { useDecisionHistory } from '../hooks/useDecisionHistory'
import { BattleInspector, type InspectorSize } from '../components/BattleInspector'
import { FactionSummaries } from '../components/FactionSummaries'
import { BattleReplay } from '../components/BattleReplay'
import { BattleHeader, BattleStatistics, battleTitleFor, Outcome, BattleControls, BattleState } from '../components/BattlePresentation'
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
  const { decisions, decisionMessage } = useDecisionHistory(id, battle, permissions.inspect)
  const [controlState, setControlState] = useState<BattleControlState>('idle')
  const [controlMessage, setControlMessage] = useState('')
  const [inspectorSize, setInspectorSize] = useState<InspectorSize>('normal')
  const [replaying, setReplaying] = useState(false)
  const inspectorRail = useRef<HTMLButtonElement>(null)
  const previousInspectorSize = useRef(inspectorSize)
  // Keep keyboard focus on the control that replaces the one just hidden.
  useEffect(() => {
    const previous = previousInspectorSize.current
    previousInspectorSize.current = inspectorSize
    if (inspectorSize === 'hidden') inspectorRail.current?.focus()
    else if (previous === 'hidden') document.getElementById('inspector-selection')?.focus()
  }, [inspectorSize])

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
  const displayView = battle.map && battle.state ? { map: battle.map, state: battle.state } : null
  const settled = ['completed', 'stopped', 'failed'].includes(battle.status)

  const showInspector = permissions.inspect
  const layout = [
    'battle-cockpit',
    displayView ? '' : 'battle-cockpit--events-only',
    showInspector ? `battle-cockpit--inspector-${inspectorSize}` : 'battle-cockpit--no-inspector',
  ].filter(Boolean).join(' ')

  return (
    <>
      <div className={layout}>
        <BattleHeader battle={battle} battleTitle={battleTitle} connection={connection} connectionMessage={connectionMessage} retryConnection={retryConnection}>
          <div className="battle-progress-panel">
            <p>Tick <strong>{battle.currentTick}</strong> of {battle.requestedTicks} maximum</p>
            <progress className="battle-progress" aria-label="Tick limit used" max={battle.requestedTicks} value={battle.currentTick}>{progress}%</progress>
            {settled && displayView && !replaying && <button className="compact-action" type="button" onClick={() => setReplaying(true)}>WATCH REPLAY</button>}
          </div>
          <aside className={`outcome-panel outcome-panel--${battle.paused ? 'paused' : battle.status}`} aria-label={settled ? 'Battle result' : 'Battle control'}>
            <div className="outcome-panel__summary"><Outcome battle={battle} winnerName={winnerName} /></div>
            {permissions.canManage(battle.ownerId) && <BattleControls battle={battle} state={controlState} message={controlMessage} onControl={runBattleControl} />}
          </aside>
        </BattleHeader>

        <BattleStage battle={battle} view={displayView} replaying={replaying} decisions={decisions} decisionMessage={decisionMessage} participantNames={participantNames} onExitReplay={() => setReplaying(false)} />
        {showInspector && inspectorSize === 'hidden' && <button ref={inspectorRail} className="inspector-rail" type="button" aria-label="Show inspector" onClick={() => setInspectorSize('normal')}><span aria-hidden="true">‹</span> INSPECTOR</button>}
        {showInspector && <BattleInspector key={`${id}:${battle.attempt}`} battle={battle} decisions={decisions} decisionMessage={decisionMessage} size={inspectorSize} onResize={setInspectorSize} events={{
          battleAttempt: battle.attempt, selectedAttempt, setSelectedAttempt, events, participantNames,
          eventCursor, eventHasMore, eventState, eventMessage, loadEventPage,
        }} />}
      </div>

      {!displayView && <BattleStatistics battle={battle} participantNames={participantNames} />}
    </>
  )
}

// The live faction strips and map, or the replay that replaces them. Replay is offered
// only once a battle settles, and a settled battle never becomes active again. A result
// covers the map until the viewer asks for the map; focus follows the swapped control.
function BattleStage({ battle, view, replaying, decisions, decisionMessage, participantNames, onExitReplay }: Readonly<{
  battle: BattleRun
  view: { map: BattleMap; state: BattleRun['state'] } | null
  replaying: boolean
  decisions: DecisionHistory[]
  decisionMessage: string
  participantNames: Map<string, string>
  onExitReplay: () => void
}>) {
  const [resultsShown, setResultsShown] = useState(true)
  const toggled = useRef(false)
  const viewMap = useRef<HTMLButtonElement>(null)
  const showResults = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    if (toggled.current) (resultsShown ? viewMap : showResults).current?.focus()
  }, [resultsShown])
  const toggleResults = (shown: boolean) => { toggled.current = true; setResultsShown(shown) }

  if (replaying && view) {
    return <BattleReplay battle={battle} map={view.map} decisions={decisions} decisionMessage={decisionMessage} participantNames={participantNames} onExit={onExitReplay} />
  }
  const overlay = !!battle.result && resultsShown
  return <>
    <FactionSummaries battle={battle} decisions={decisions} decisionMessage={decisionMessage} />
    {view && <div className={`battle-map-column${overlay ? ' battle-map-column--results' : ''}`}>
      <HexBattleMap map={view.map} state={view.state} factionNames={participantNames}
        actions={battle.result && !resultsShown && <button ref={showResults} className="map-toolbar__results" type="button" onClick={() => toggleResults(true)}>Show results</button>} />
      {overlay && <BattleStatistics battle={battle} participantNames={participantNames} onShowMap={() => toggleResults(false)} showMapRef={viewMap} />}
    </div>}
  </>
}
