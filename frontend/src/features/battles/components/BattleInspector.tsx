import { useState, type ComponentProps } from 'react'
import type { BattleRun, DecisionHistory, SavedDecisionContext } from '../api/battles'
import { AIDecisionPanel, controllerLabel, decisionLabel, latestDecision } from './AIDecisionPanel'
import { EventConsole } from './EventConsole'
import { entityIndex } from '../presentation/entities'
import { InspectorTabs } from './InspectorTabs'
import { playerStatus, PlayerMetrics, PlayerOverview, PlayerUnits } from './PlayerStatusPanel'
import { productionSummary } from '../presentation/production'

function ProductionSchedule({ battle }: Readonly<{ battle: BattleRun }>) {
  const summary = productionSummary(battle)
  return summary && <p className="production-schedule" aria-label="Production schedule">{summary}</p>
}

export type InspectorSize = 'normal' | 'wide' | 'hidden'

const sections = ['overview', 'decisions', 'units', 'faction'] as const
const sectionTabs = sections.map((id) => ({ id, label: id[0].toUpperCase() + id.slice(1) }))

export function BattleInspector({ battle, decisions, decisionMessage, events, size = 'normal', onResize }: Readonly<{
  battle: BattleRun; decisions: DecisionHistory[]; decisionMessage: string; events: ComponentProps<typeof EventConsole>
  size?: InspectorSize; onResize?: (size: InspectorSize) => void
}>) {
  const [selected, setSelected] = useState('system')
  const [section, setSection] = useState('overview')
  const [cache] = useState(() => new Map<string, SavedDecisionContext>())
  const entities = entityIndex(battle.state)
  const names = new Map(battle.participants.map((player) => [player.id, player.name]))
  return <aside className="battle-inspector" aria-label="Battle inspector" hidden={size === 'hidden'}>
    <div className="inspector-toolbar">
      <label className="inspector-selector" htmlFor="inspector-selection">
        <span>Inspect</span>
        <select id="inspector-selection" aria-controls="inspector-content" value={selected} onChange={(event) => setSelected(event.target.value)}>
          <option value="system">System</option>
          {battle.participants.map((player) => <option key={player.id} value={player.id}>{player.name}</option>)}
        </select>
      </label>
      {onResize && <div className="inspector-size-controls">
        <button type="button" aria-pressed={size === 'wide'} title={size === 'wide' ? 'Narrow inspector' : 'Widen inspector'} onClick={() => onResize(size === 'wide' ? 'normal' : 'wide')}>
          <svg className="inspector-size-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={size === 'wide' ? 'M3 12H10M7 8 10 12 7 16M21 12H14M17 8 14 12 17 16' : 'M3 12H10M6 8 3 12 6 16M21 12H14M18 8 21 12 18 16'} /></svg><span className="visually-hidden">Widen inspector</span>
        </button>
        <button type="button" title="Hide inspector" onClick={() => onResize('hidden')}>
          <svg className="inspector-size-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M8 6 14 12 8 18M19 5V19" /></svg><span className="visually-hidden">Hide inspector</span>
        </button>
      </div>}
    </div>
    {battle.participants.map((player) => {
      const status = playerStatus(battle.state, player.id)
      return <div key={player.id} hidden={selected !== player.id} className="inspector-player-heading">
        <h2 id={`inspector-player-title-${player.id}`}>{player.name}</h2><p>{controllerLabel(player.controller)} · {decisionLabel(latestDecision(decisions, player.id))}</p>
        <span className="snapshot-label">{battle.state ? `CURRENT SPECTATOR SNAPSHOT · TICK ${battle.state.tick}` : 'AWAITING STATE'}</span>
        <PlayerMetrics status={status} />
        <InspectorTabs id={`player-${player.id}`} label={`${player.name} information`} tabs={sectionTabs} selected={section} onSelect={setSection} />
      </div>
    })}
    <div id="inspector-content" className="inspector-scroll">
      <section id="inspector-panel-system" aria-label="System information" hidden={selected !== 'system'}>
        <ProductionSchedule battle={battle} />
        <table className="player-comparison"><caption>Current player comparison{battle.state ? ` · tick ${battle.state.tick}` : ' · awaiting state'}</caption><thead><tr><th>Player</th><th>Gold</th><th>Units</th><th>Castle HP</th></tr></thead><tbody>
          {battle.participants.map((player) => {
            const status = playerStatus(battle.state, player.id)
            return <tr key={player.id}><th><button className="text-button" type="button" onClick={() => { setSelected(player.id); document.getElementById('inspector-selection')?.focus() }}>{player.name}</button></th><td>{status.faction?.gold ?? '—'}</td><td>{status.faction ? status.units.length : '—'}</td><td>{status.castleHealth}</td></tr>
          })}
        </tbody></table>
        <EventConsole {...events} entities={entities} />
      </section>
      {battle.participants.map((player) => {
        const status = playerStatus(battle.state, player.id)
        const panelId = `player-${player.id}`
        return <section key={player.id} id={`inspector-panel-${player.id}`} aria-labelledby={`inspector-player-title-${player.id}`} hidden={selected !== player.id}>
          {sections.map((item) => <div key={item} id={`${panelId}-panel-${item}`} role="tabpanel" aria-labelledby={`${panelId}-tab-${item}`} hidden={section !== item} tabIndex={0} className="inspector-section">
            {item === 'overview' && <PlayerOverview status={status} />}
            {item === 'units' && <PlayerUnits status={status} />}
            {item === 'decisions' && <AIDecisionPanel entities={entities} decisions={decisions} participantNames={names} message={decisionMessage} hasModelController={player.controller?.type === 'model'} selectedFaction={player.id} attempt={battle.attempt} battleId={battle.id} active={selected === player.id && section === 'decisions'} contextCache={cache} embedded />}
            {item === 'faction' && <section aria-label="Saved faction profile"><h3>Background</h3><p className="faction-prose">{player.background || 'No background saved with this battle.'}</p><h3>Doctrine</h3><p className="faction-prose">{player.doctrine || 'No doctrine saved with this battle.'}</p><h3>Controller</h3><p>{controllerLabel(player.controller)}</p><p>{player.controller?.type ?? 'scripted'}{player.controller?.provider ? ` · ${player.controller.provider}` : ''}{player.controller?.runtime ? ` · ${player.controller.runtime}` : ''}</p><p>Faction profile saved when this battle was created.</p></section>}
          </div>)}
        </section>
      })}
    </div>
  </aside>
}
