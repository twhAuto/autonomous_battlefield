import type { BattleBuilding, BattleState } from '../api/battles'
import { entityName, typeLabel } from '../presentation/entities'
import { EntityIcon } from './EntityIcon'

export function BuildingProductionPanel({ state, factionNames, paused, settled }: Readonly<{
  state: BattleState; factionNames: Map<string, string>; paused: boolean; settled: boolean
}>) {
  const buildings = (state.buildings ?? []).filter((b) => b.health > 0 && (b.production || b.trains?.length || b.trainingQueue?.length))
  if (!buildings.length) return null
  const factions = [...new Set(buildings.map((b) => b.factionId))]
  return <section className="building-production" aria-label="Building production">
    <h2>Building production</h2>
    <p>{battleProductionNote(paused, settled)}</p>
    {factions.map((id) => <section key={id} aria-label={`${factionNames.get(id) ?? id} production`}>
      <h3>{factionNames.get(id) ?? id}</h3>
      <ul className="production-buildings">{buildings.filter((b) => b.factionId === id).map((b) => <li key={b.id}>
        <h4><EntityIcon type={b.type} />{entityName(b.id, { localFactionId: id })} <small>({b.position.x}, {b.position.y})</small></h4>
        {b.readyAtTick !== undefined && b.readyAtTick > state.tick && <p>Activates at tick {b.readyAtTick}</p>}
        <TrainingQueue building={b} />
      </li>)}</ul>
    </section>)}
  </section>
}

function ContinuousTraining({ building }: Readonly<{ building: BattleBuilding }>) {
  const production = building.production!
  const current = building.trainingQueue?.[0]
  return <div className="training-queue production-status">
    <strong>{production.enabled ? 'On' : 'Off'} · {production.unitType ? <><EntityIcon type={production.unitType} />{typeLabel(production.unitType)}</> : 'No unit selected'}</strong>
    {current ? <>
      <span>{typeLabel(current.unitType)} · {building.trainingBlocked ? 'Ready · exit blocked' : `${current.remainingTicks}/${current.trainingTicks} ticks remaining`}</span>
      <progress aria-label={`${typeLabel(current.unitType)} training progress`} max={current.trainingTicks} value={current.trainingTicks - current.remainingTicks} />
      {!production.enabled && <span>Finishing paid unit, then stopping</span>}
      {production.enabled && production.unitType && production.unitType !== current.unitType && <span>Next: {typeLabel(production.unitType)}</span>}
    </> : <span>{idleProductionStatus(production)}</span>}
  </div>
}

export function TrainingQueue({ building }: Readonly<{ building: BattleBuilding }>) {
  if (building.production) return <ContinuousTraining building={building} />
  if (!building.trains?.length && !building.trainingQueue?.length) return null
  const queue = building.trainingQueue ?? []
  if (!queue.length) return <span>Training queue empty</span>
  return <div className="training-queue">
    <strong>Training queue · {queue.length}/12</strong>
    <ol>{queue.map((order, index) => <li key={`${order.unitType}:${order.queuedAtTick}:${index}`}>
      {typeLabel(order.unitType)} · {index === 0 && building.trainingBlocked ? 'Ready · exit blocked' : `${order.remainingTicks}/${order.trainingTicks} ticks remaining`}
      {index === 0 && <progress aria-label="Training progress" max={order.trainingTicks} value={order.trainingTicks - order.remainingTicks} />}
    </li>)}</ol>
  </div>
}

function battleProductionNote(paused: boolean, settled: boolean) {
  if (settled) return 'Battle ended · final production state.'
  if (paused) return 'Battle paused · training is paused.'
  return 'AI controls production. Each building trains one unit at a time.'
}

function idleProductionStatus(production: NonNullable<BattleBuilding['production']>) {
  if (!production.enabled) return 'Production stopped'
  if (production.waitingForUnitLimit) return 'Waiting · faction unit limit reached'
  return production.waitingForGold ? 'Waiting for gold · respects AI reserve' : 'Waiting to start'
}
