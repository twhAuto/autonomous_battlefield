import type { BattleBuilding, BattleState } from '../api/battles'
import { entityName, typeLabel } from '../presentation/entities'
import { EntityIcon } from './EntityIcon'

// Compact per-building production chips for a faction strip. Each chip is a disclosure,
// so the full status is available by hover, keyboard, touch and screen reader. Chips
// sharing `group` close each other when opened.
export function ProductionChips({ state, factionId, factionName, group }: Readonly<{
  state: BattleState; factionId: string; factionName: string; group?: string
}>) {
  const buildings = (state.buildings ?? []).filter((b) => b.factionId === factionId && b.health > 0 && (b.production || b.trains?.length || b.trainingQueue?.length))
  if (!buildings.length) return null
  return <ul className="production-chips" aria-label={`${factionName} production`}>
    {buildings.map((b) => <ProductionChip key={b.id} building={b} tick={state.tick} group={group} />)}
  </ul>
}

function ProductionChip({ building, tick, group }: Readonly<{ building: BattleBuilding; tick: number; group?: string }>) {
  const current = building.trainingQueue?.[0]
  const unitType = current?.unitType ?? building.production?.unitType
  const { tone, mark } = productionTone(building, tick)
  const description = `${entityName(building.id, { localFactionId: building.factionId })} (${building.position.x}, ${building.position.y}) · ${productionDetail(building, tick)}`
  return <li className={`production-chip production-chip--${tone}`} title={description}>
    <details name={group}>
      <summary>
        <EntityIcon type={building.type} />
        {unitType && <><span className="production-chip__arrow" aria-hidden="true">›</span><EntityIcon type={unitType} /></>}
        {mark && <span className="production-chip__mark" aria-hidden="true">{mark}</span>}
        {current && <progress aria-label={`${typeLabel(current.unitType)} training progress`} max={current.trainingTicks} value={current.trainingTicks - current.remainingTicks} />}
        <span className="visually-hidden">{description}</span>
      </summary>
      <p className="production-chip__detail" aria-hidden="true">{description}</p>
    </details>
  </li>
}

function productionTone(building: BattleBuilding, tick: number): { tone: string; mark?: string } {
  if (building.readyAtTick !== undefined && building.readyAtTick > tick) return { tone: 'inactive', mark: `T${building.readyAtTick}` }
  if (building.trainingBlocked) return { tone: 'blocked', mark: '!' }
  if (building.trainingQueue?.length) return { tone: 'training' }
  const production = building.production
  if (!production) return { tone: 'idle', mark: '–' }
  if (!production.enabled) return { tone: 'off', mark: 'off' }
  if (production.waitingForUnitLimit) return { tone: 'waiting', mark: 'max' }
  if (production.waitingForGold) return { tone: 'waiting', mark: '$' }
  return { tone: 'idle', mark: '…' }
}

export function productionDetail(building: BattleBuilding, tick: number) {
  const parts: string[] = []
  if (building.readyAtTick !== undefined && building.readyAtTick > tick) parts.push(`Activates at tick ${building.readyAtTick}`)
  const queue = building.trainingQueue ?? []
  const current = queue[0]
  const progress = current && (building.trainingBlocked ? 'Ready · exit blocked' : `${current.remainingTicks}/${current.trainingTicks} ticks remaining`)
  const production = building.production
  if (!production) {
    parts.push(current ? `Training queue · ${queue.length}/12 · ${typeLabel(current.unitType)} · ${progress}` : 'Training queue empty')
    return parts.join(' · ')
  }
  parts.push(`${production.enabled ? 'On' : 'Off'} · ${production.unitType ? typeLabel(production.unitType) : 'No unit selected'}`)
  if (!current) parts.push(idleProductionStatus(production))
  else {
    parts.push(`${typeLabel(current.unitType)} · ${progress}`)
    if (!production.enabled) parts.push('Finishing paid unit, then stopping')
    else if (production.unitType && production.unitType !== current.unitType) parts.push(`Next: ${typeLabel(production.unitType)}`)
  }
  return parts.join(' · ')
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

function idleProductionStatus(production: NonNullable<BattleBuilding['production']>) {
  if (!production.enabled) return 'Production stopped'
  if (production.waitingForUnitLimit) return 'Waiting · faction unit limit reached'
  return production.waitingForGold ? 'Waiting for gold · respects AI reserve' : 'Waiting to start'
}
