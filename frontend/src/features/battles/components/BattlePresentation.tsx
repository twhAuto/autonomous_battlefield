// Presentational pieces for battle headings, connection state, statistics, outcomes, and
// controls; data loading stays in the page and hooks.

import { type ReactNode, type Ref } from 'react'
import { Link } from 'react-router-dom'
import { BattleRun } from '../api/battles'
import { matchLabel } from '../../../shared/domain/leagueMatches'
import { modelLabel } from '../presentation/factions'
import { type ConnectionState, type BattleControlState, type BattleControlAction } from '../types'
import { TypeStatistics } from './TypeStatistics'

// Single command band: title, scenario, league and connection on the left; the
// page supplies tick progress, outcome and controls as children. The title already states
// the battle's status, so a settled battle shows no connection badge.
export function BattleHeader({ battle, battleTitle, connection, connectionMessage, retryConnection, children }: Readonly<{
  battle: BattleRun
  battleTitle: string
  connection: ConnectionState
  connectionMessage: string
  retryConnection: () => void
  children?: ReactNode
}>) {
  return (
    <section className="battle-command-bar" aria-label="Battle status">
      <div className="battle-command-bar__title">
        <h1>{battleTitle}</h1>
      </div>
      <div className="battle-command-bar__meta">
        <div className="battle-meta-row">
          <span>SCENARIO <strong>{battle.scenarioId}</strong></span>
          {battle.ownerName && <span className="battle-owner">Started by <strong>{battle.ownerName}</strong></span>}
          {battle.league && <span>LEAGUE <Link className="text-link" to={`/leagues/${encodeURIComponent(battle.league.id)}`}>{battle.league.name}</Link> · {matchLabel(battle.league).toUpperCase()}</span>}
          {connection !== 'settled' && <ConnectionBadge state={connection} />}
        </div>
        {connectionMessage && (
          <output className="connection-message">
            <span>{connectionMessage}</span>
            {connection === 'error' && <button className="text-button" type="button" onClick={retryConnection}>RETRY CONNECTION</button>}
          </output>
        )}
      </div>
      {children}
    </section>
  )
}

// With `onShowMap` the statistics cover the map. Their header is a map toolbar led by the
// control that reveals the map, exactly where the map's toolbar leads with Show results.
export function BattleStatistics({ battle, participantNames, onShowMap, showMapRef }: Readonly<{
  battle: BattleRun
  participantNames: Map<string, string>
  onShowMap?: () => void
  showMapRef?: Ref<HTMLButtonElement>
}>) {
  if (!battle.result) return null
  const { winnerFactionId } = battle.result
  return (
    <section className={`battle-statistics${onShowMap ? ' battle-statistics--overlay' : ''}`} aria-label="After-action statistics">
      {onShowMap
        ? <div className="map-toolbar">
          <button ref={showMapRef} className="map-toolbar__results" type="button" onClick={onShowMap}>Show map</button>
          <span>AFTER-ACTION STATISTICS · {battle.result.ticksRun} TICKS</span>
        </div>
        : <div className="section-label"><span>AFTER-ACTION STATISTICS</span><span>{battle.result.ticksRun} TICKS</span></div>}
      <div className="statistics-grid">
        {battle.result.factions.map((stats) => (
          <article key={stats.factionId} className="statistics-card">
            <h2>
              {participantNames.get(stats.factionId) ?? stats.factionId}
              <FactionModel battle={battle} factionId={stats.factionId} />
              {stats.factionId === winnerFactionId && <>{' '}<span className="statistics-card__winner">Winner</span></>}
            </h2>
            <dl>
              <Stat label="Units remaining / started" value={`${stats.unitsRemaining} / ${stats.unitsStarted}`} />
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
            <TypeStatistics stats={stats} factionName={participantNames.get(stats.factionId) ?? stats.factionId} />
          </article>
        ))}
      </div>
    </section>
  )
}

export function battleTitleFor(battle: BattleRun) {
  if (battle.status === 'completed') return 'Battle complete'
  if (battle.status === 'failed') return 'Battle failed'
  if (battle.status === 'stopped') return 'Battle stopped'
  if (battle.paused) return 'Battle paused'
  return 'Battle in progress'
}

export function Outcome({ battle, winnerName }: Readonly<{ battle: BattleRun; winnerName: string | null }>) {
  if (battle.status === 'completed' && battle.result) {
    return (
      <>
        <strong>{winnerName ? <>{winnerName} wins<FactionModel battle={battle} factionId={battle.result.winnerFactionId} /></> : 'Draw'}</strong>
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

export function BattleControls({ battle, state, message, onControl }: Readonly<{
  battle: BattleRun
  state: BattleControlState
  message: string
  onControl: (action: BattleControlAction) => Promise<void>
}>) {
  const active = battle.status === 'queued' || battle.status === 'running'
  const busy = state === 'pausing' || state === 'resuming' || state === 'stopping'
  if (!active) return null
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

export function BattleState({ code, title, message, loading, children }: Readonly<{ code: string; title: string; message: string; loading?: boolean; children?: ReactNode }>) {
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

// The faction's model beside its name in results; nothing for scripted factions or players.
function FactionModel({ battle, factionId }: Readonly<{ battle: BattleRun; factionId: string | null }>) {
  const model = modelLabel(battle.participants.find((participant) => participant.id === factionId))
  return model ? <>{' '}<small className="faction-model">{model}</small></> : null
}

function Stat({ label, value }: Readonly<{ label: string; value: string | number }>) {
  return <div><dt>{label}</dt><dd>{value}</dd></div>
}
