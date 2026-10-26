// Shared live spectator summaries for the comparison table and selected player.
import type { BattleState } from '../api/battles'
import { entityIndex, entityName, typeLabel } from '../presentation/entities'
import { ownedUpgradeLabels } from '../presentation/upgrades'
import { EntityIcon } from './EntityIcon'

export function playerStatus(state: BattleState | undefined, factionId: string) {
  const faction = state?.factions?.find((entry) => entry.factionId === factionId)
  const units = state?.units?.filter((unit) => unit.factionId === factionId && unit.health > 0) ?? []
  const buildings = state?.buildings?.filter((building) => building.factionId === factionId && building.health > 0) ?? []
  const castle = buildings.find((building) => building.type === 'castle')
  let castleHealth = '—'
  if (faction) castleHealth = castle ? `${castle.health} / ${castle.maxHealth}` : 'Destroyed'
  return { faction, units, buildings, castleHealth }
}

type PlayerStatus = ReturnType<typeof playerStatus>

export function PlayerMetrics({ status }: Readonly<{ status: PlayerStatus }>) {
  return <dl className="player-status-metrics">
    <div><dt>Gold</dt><dd>{status.faction?.gold ?? '—'}</dd></div>
    <div><dt>Units on field</dt><dd>{status.faction ? status.units.length : '—'}</dd></div>
    <div><dt>Castle HP</dt><dd>{status.castleHealth}</dd></div>
  </dl>
}

export function PlayerOverview({ status }: Readonly<{ status: PlayerStatus }>) {
  const { faction, buildings } = status
  const groups = new Map<string, number>()
  for (const building of buildings) groups.set(building.type, (groups.get(building.type) ?? 0) + 1)
  return <section aria-label="Player overview">
    <h3>Economy</h3>
    <dl className="inspector-facts">
      <div><dt>Gold earned</dt><dd>{faction?.goldEarned ?? '—'}</dd></div>
      <div><dt>Gold spent</dt><dd>{faction?.goldSpent ?? '—'}</dd></div>
      <div><dt>Gold looted</dt><dd>{faction?.goldLooted ?? '—'}</dd></div>
      <div><dt>Buildings</dt><dd>{faction ? buildings.length : '—'}</dd></div>
    </dl>
    <h3>Buildings by type</h3>
    {faction ? <div className="building-type-list">{groups.size ? [...groups].sort(([a], [b]) => a.localeCompare(b)).map(([type, count]) => <span key={type}><EntityIcon type={type} />{count} × {typeLabel(type)}</span>) : <p>No living buildings</p>}</div> : <p>Waiting for player state.</p>}
    <h3>Owned upgrades</h3><p>{faction ? ownedUpgradeLabels(faction.ownedUpgradeIds) : '—'}</p>
  </section>
}

export function PlayerUnits({ status }: Readonly<{ status: PlayerStatus }>) {
  const naming = { entities: entityIndex(status), localFactionId: status.faction?.factionId }
  const types = [...new Set(status.units.map((unit) => unit.type))].sort((a, b) => a.localeCompare(b))
  return <section aria-label="Fielded units">
    <h3>Living army</h3>
    {!status.faction && <p>Waiting for player state.</p>}
    {status.faction && types.length === 0 && <p>No units on the field</p>}
    {types.map((type) => {
      const units = status.units.filter((unit) => unit.type === type)
      return <details className="unit-group" key={type}><summary><EntityIcon type={type} />{units.length} × {typeLabel(type)}</summary>
        <table><caption>{typeLabel(type)} on the field</caption><thead><tr><th>Unit</th><th>HP</th><th>Position</th></tr></thead><tbody>
          {units.map((unit) => <tr key={unit.id}><td title={unit.id}>{entityName(unit.id, naming)}</td><td>{unit.health} / {unit.maxHealth}</td><td>({unit.position.x}, {unit.position.y})</td></tr>)}
        </tbody></table>
      </details>
    })}
  </section>
}
