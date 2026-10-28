// Replaces the live faction strips and map with a recorded replay: playback controls
// above the frame's battlefield.

import type { BattleMap, BattleRun, DecisionHistory } from '../api/battles'
import { replaySpeeds, useBattleReplay, type BattleReplayControls, type ReplaySpeed } from '../hooks/useBattleReplay'
import { FactionSummaries } from './FactionSummaries'
import { HexBattleMap } from './HexBattleMap'

export function BattleReplay({ battle, map, decisions, decisionMessage, participantNames, onExit }: Readonly<{
  battle: BattleRun
  map: BattleMap
  decisions: DecisionHistory[]
  decisionMessage: string
  participantNames: Map<string, string>
  onExit: () => void
}>) {
  const replay = useBattleReplay(battle.id, battle.currentTick)
  const frame = replay.frame
  return <>
    {frame
      ? <FactionSummaries battle={{ ...battle, currentTick: frame.tick, state: frame.state }} decisions={decisions} decisionMessage={decisionMessage} />
      : <section className="faction-summaries" aria-label="Faction summaries" />}
    <div className="battle-map-column">
      <ReplayControls replay={replay} finalTick={battle.currentTick} onExit={onExit} />
      {frame
        ? <HexBattleMap map={map} state={frame.state} factionNames={participantNames} />
        : !replay.error && <output className="replay-loading">Loading replay…</output>}
    </div>
  </>
}

function ReplayControls({ replay, finalTick, onExit }: Readonly<{ replay: BattleReplayControls; finalTick: number; onExit: () => void }>) {
  return <section className="replay-controls" aria-label="Replay controls">
    <div className="replay-controls__buttons">
      <button type="button" aria-label="Restart replay" disabled={replay.tick === 0} onClick={() => { replay.pause(); replay.seek(0) }}>⏮</button>
      <button type="button" aria-label="Previous tick" disabled={replay.tick === 0} onClick={() => replay.step(-1)}>‹</button>
      {replay.playing
        ? <button type="button" className="replay-controls__play" onClick={replay.pause}>PAUSE</button>
        : <button type="button" className="replay-controls__play" disabled={!replay.frame} onClick={replay.play}>PLAY</button>}
      <button type="button" aria-label="Next tick" disabled={replay.tick >= finalTick} onClick={() => replay.step(1)}>›</button>
    </div>
    <label className="replay-controls__timeline">
      <span>Replay tick</span>
      <input type="range" min={0} max={finalTick} value={replay.tick} onChange={(event) => replay.seek(Number(event.target.value))} />
    </label>
    <output className="replay-controls__tick" aria-live="off">TICK <strong>{replay.tick}</strong> / {finalTick}{replay.buffering ? ' · BUFFERING' : ''}</output>
    <label className="replay-controls__speed">
      <span>Speed</span>
      <select value={replay.speed} onChange={(event) => replay.setSpeed(Number(event.target.value) as ReplaySpeed)}>
        {replaySpeeds.map((speed) => <option key={speed} value={speed}>{speed}×</option>)}
      </select>
    </label>
    <button type="button" className="text-button" onClick={onExit}>EXIT REPLAY</button>
    {replay.error && <div className="replay-controls__error" role="alert">{replay.error} <button type="button" onClick={replay.retry}>Retry</button></div>}
  </section>
}
