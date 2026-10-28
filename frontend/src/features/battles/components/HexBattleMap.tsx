// Renders an interactive SVG hex battlefield from supplied state, including terrain,
// each faction's build area, entity selection, and map navigation.

import { type CSSProperties, type KeyboardEvent, type PointerEvent, type ReactNode, useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { useMobile } from '../../../shared/hooks/useMobile'
import type { BattleBuilding, BattleMap, BattleState, BattleUnit, HexPosition } from '../api/battles'
import { TrainingQueue } from './BuildingProduction'
import { TerrainGlyph } from './TerrainGlyph'
import { entityIndex, entityName, typeLabel, type EntityNaming } from '../presentation/entities'
import { EntityGlyph, EntityLegend } from './EntityIcon'
import { factionColors } from '../presentation/factions'

type Entity = BattleUnit | BattleBuilding
const size = 24
const root3 = Math.sqrt(3)

/**
 * `actions` adds page controls to the toolbar; the expanded dialog omits them. `covered`
 * makes the inline map inert while page content lies over it; an open dialog stays usable,
 * since a live battle can settle while it is open.
 */
export function HexBattleMap({ map, state, factionNames, actions, covered = false }: Readonly<{
  map: BattleMap
  state: BattleState
  factionNames: Map<string, string>
  actions?: ReactNode
  covered?: boolean
}>) {
  const markerId = useId()
  const mobile = useMobile()
  const [expanded, setExpanded] = useState(false)
  const dialog = useRef<HTMLDialogElement>(null)
  const expandButton = useRef<HTMLButtonElement>(null)
  const viewport = useRef<HTMLElement>(null)
  const entityNodes = useRef(new Map<string, SVGGElement>())
  const [scale, setScale] = useState(1)
  const [pan, setPan] = useState({ x: 24, y: 30 })
  const [drag, setDrag] = useState<{ x: number; y: number } | null>(null)
  // Where the current press started and whether it has moved far enough to be a pan.
  const press = useRef<{ x: number; y: number; panned: boolean } | null>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const liveUnits = (state.units ?? []).filter((unit) => unit.health > 0)
  const entities: Entity[] = [...(state.buildings ?? []).filter((building) => building.health > 0), ...liveUnits]
  const naming = { entities: entityIndex(state), factionNames }
  const selected = entities.find((entity) => entity.id === selectedId)
  const roster = factionNames.size > 0 ? [...factionNames.keys()] : (state.factions ?? []).map((faction) => faction.factionId)
  const colors = factionColors(roster)
  const areas = buildAreas(map, state)
  const builders = new Map<string, string[]>()
  for (const [factionId, positions] of areas) {
    for (const position of positions) builders.set(key(position), [...(builders.get(key(position)) ?? []), factionNames.get(factionId) ?? factionId])
  }
  const width = root3 * size * (map.width + 0.5) + 48
  const height = size * (1.5 * (map.height - 1) + 2) + 48

  function fit() { setScale(1); setPan({ x: 24, y: 30 }); setDrag(null) }
  useEffect(() => {
    fit()
    if (typeof ResizeObserver === 'undefined' || !viewport.current) return
    const observer = new ResizeObserver(fit)
    observer.observe(viewport.current)
    return () => observer.disconnect()
  }, [expanded, width, height])
  useEffect(() => {
    if (!expanded || !dialog.current) return
    const element = dialog.current
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    element.showModal()
    return () => {
      element.close()
      document.body.style.overflow = previousOverflow
      expandButton.current?.focus()
    }
  }, [expanded])

  function pointerMove(event: PointerEvent<SVGSVGElement>) {
    if (press.current && Math.hypot(event.clientX - press.current.x, event.clientY - press.current.y) > 4) press.current.panned = true
    if (!drag) return
    const bounds = event.currentTarget.getBoundingClientRect()
    const ratio = Math.min(bounds.width / width, bounds.height / height) || 1
    setPan((current) => ({ x: current.x + (event.clientX - drag.x) / ratio, y: current.y + (event.clientY - drag.y) / ratio }))
    setDrag({ x: event.clientX, y: event.clientY })
  }
  function zoom(delta: number) { setScale((current) => Math.max(0.55, Math.min(2.4, current + delta))) }

  function trapFocus(event: KeyboardEvent<HTMLDialogElement>) {
    if (event.key !== 'Tab') return
    const controls = Array.from(event.currentTarget.querySelectorAll<HTMLElement>('a[href], button, input, select, textarea, summary, [tabindex]'))
      .filter(node => node.tabIndex >= 0 && !node.matches(':disabled') && node.getClientRects().length > 0)
    const first = controls[0]
    const last = controls.at(-1)
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
  }

  const content = (
    <section className="map-panel" aria-label="Castle siege map" inert={covered && !expanded}>
      <div className="map-toolbar">
        <span>HEX TACTICAL MAP · TICK {state.tick}</span>
        <div className="map-toolbar__controls">
          {!expanded && actions}
          <div className="map-zoom-controls"><button type="button" aria-label="Pan map with arrow keys" onKeyDown={event => {
              if (event.target !== event.currentTarget) return
              const offset: Record<string, [number, number]> = { ArrowLeft: [24, 0], ArrowRight: [-24, 0], ArrowUp: [0, 24], ArrowDown: [0, -24] }
              if (offset[event.key]) {
                event.preventDefault()
                const [x, y] = offset[event.key]
                setPan(current => ({ x: current.x + x, y: current.y + y }))
              }
            }}>Pan with arrow keys</button><button type="button" onClick={() => zoom(-0.15)} aria-label="Zoom out">−</button><button type="button" onClick={() => zoom(0.15)} aria-label="Zoom in">+</button><button type="button" onClick={fit}>Fit map</button>
            {expanded ? <button type="button" autoFocus onClick={() => setExpanded(false)}>Close map</button> : <button ref={expandButton} type="button" onClick={() => setExpanded(true)}>Expand map</button>}
          </div>
        </div>
      </div>
      <section ref={viewport} className={`map-viewport${mobile && !expanded ? ' map-viewport--overview' : ''}`} style={expanded ? undefined : { '--map-aspect': `${width} / ${height}` } as CSSProperties} aria-label="Map navigation">
        <svg
          viewBox={`0 0 ${width} ${height}`}
          onPointerDown={(event) => {
            press.current = { x: event.clientX, y: event.clientY, panned: false }
            if (mobile && !expanded) return
            event.currentTarget.setPointerCapture?.(event.pointerId)
            setDrag({ x: event.clientX, y: event.clientY })
          }}
          onPointerMove={pointerMove}
          onPointerUp={() => setDrag(null)}
          onPointerCancel={() => setDrag(null)}
          onClick={(event) => {
            // Clicking empty ground clears the selection; the click that ends a pan does not.
            if (press.current?.panned || (event.target as Element).closest('.map-entity')) return
            setSelectedId(null)
          }}
        >
          <title>{map.width} by {map.height} pointy-top hex battlefield</title>
          <defs>
            {[...new Set([...colors.values(), '#f2c94c'])].map((color) => <marker key={color} id={`${markerId}-${color.slice(1)}`} fill={color} markerUnits="userSpaceOnUse" markerWidth="4.5" markerHeight="5.5" refX="4" refY="2.5" orient="auto" viewBox="0 0 4 5"><path d="M0,0 L4,2.5 L0,5 L1,2.5 Z" /></marker>)}
          </defs>
          <g className="map-layer" transform={`translate(${pan.x} ${pan.y}) scale(${scale})`}>
            {map.rows.map((row, y) => [...row].map((symbol, x) => {
              const position = { x, y }
              const center = centerOf(position)
              const buildable = builders.get(key(position))
              return <g key={`${x}:${y}`}><polygon points={hexPoints(center.x, center.y)} className={`hex-tile hex-tile--${terrainClass(symbol)}${selected && 'income' in selected && selected.health > 0 && hexDistance(selected.position, position) <= (selected.buildRange || 3) ? ' hex-tile--build-range' : ''}`}><title>{terrainDescription(symbol)} · ({x}, {y}){buildable ? ` · ${buildable.join(' and ')} can build here` : ''}</title></polygon><g transform={`translate(${center.x - 12} ${center.y - 12})`}><TerrainGlyph symbol={symbol} /></g></g>
            }))}
            {[...areas].map(([factionId, positions]) => <path key={`area:${factionId}`} className="build-area" fill={colors.get(factionId) ?? '#f2c94c'} aria-hidden="true"
              d={positions.map((position) => { const center = centerOf(position); return `M${hexPoints(center.x, center.y).replaceAll(' ', 'L')}Z` }).join('')} />)}
            {liveUnits.filter((unit) => unit.movedFrom).map((unit) => {
              const route = unit.movementPath?.length ? unit.movementPath : [unit.movedFrom!, unit.position]
              const previous = route.at(-2) ?? unit.movedFrom!
              const line = actionLine(previous, unit.position, route.length === 2 ? 2 : 0, actionClearance(previous, unit.position, entities))
              if (!line) return null
              const start = directionalLine(route[0], route[1], 2, 0)
              const points = [{ x: start.x1, y: start.y1 }, ...route.slice(1, -1).map(centerOf), { x: line.x2, y: line.y2 }]
              return <polyline key={`move:${unit.id}`} className="unit-direction unit-direction--movement" style={{ stroke: colors.get(unit.factionId) ?? '#f2c94c' }} fill="none" points={points.map((point) => `${point.x},${point.y}`).join(' ')} markerEnd={`url(#${markerId}-${(colors.get(unit.factionId) ?? '#f2c94c').slice(1)})`}><title>{entityName(unit.id, naming)} moved from {unit.movedFrom!.x}, {unit.movedFrom!.y} to {unit.position.x}, {unit.position.y}</title></polyline>
            })}
            {entities.filter((unit) => unit.health > 0 && unit.attackTargetPosition).map((unit) => {
              const target = entities.find((entity) => entity.id === unit.attackTargetId && entity.health > 0)
              const targetLabel = unit.attackOutcome === 'miss' ? `hex ${unit.attackTargetPosition!.x}, ${unit.attackTargetPosition!.y}` : entityName(unit.attackTargetId, naming)
              return <AttackVisual key={`${state.battleId}:${state.tick}:${unit.id}`} unit={unit} target={target} entities={entities} color={colors.get(unit.factionId) ?? '#f2c94c'} markerId={markerId} nodes={entityNodes.current} label={`${entityName(unit.id, naming)} ${unit.attackOutcome === 'miss' ? 'missed' : 'attacked'} ${targetLabel}`} />
            })}
            {liveUnits.filter((unit) => unit.healTargetPosition).map((unit) => {
              const line = directionalLine(unit.position, unit.healTargetPosition!, 12, 14)
              return <line key={`heal:${unit.id}`} className="unit-direction unit-direction--healing" {...line}><title>{entityName(unit.id, naming)} {unit.repairAmount ? 'repaired' : 'treated'} {entityName(unit.healTargetId, naming)}</title></line>
            })}
            {(state.unitDeaths ?? []).map((death) => {
              const center = centerOf(death.position)
              const label = `${entityName(death.id, { factionNames, entities: new Map([[death.id, death]]) })} died`
              return <g key={`death:${state.battleId}:${state.tick}:${death.id}`} className="unit-death" style={{ stroke: colors.get(death.factionId) ?? '#f2c94c' }} role="img" aria-label={label} transform={`translate(${center.x} ${center.y})`}>
                <title>{label}</title><circle r="10" /><path d="M-4,-4 L4,4 M-4,4 L4,-4" />
              </g>
            })}
            {entities.map((entity) => {
              const isBuilding = 'income' in entity
              const color = colors.get(entity.factionId) ?? '#f2c94c'
              const healthWidth = `${26 * Math.max(0, Math.min(1, entity.health / entity.maxHealth))}px`
              return <MovingEntity key={`${state.battleId}:${entity.id}`} entity={entity} tick={state.tick} nodes={entityNodes.current}><AttackNudge entity={entity} tick={state.tick} target={entities.find((target) => target.id === entity.attackTargetId)}><g className="map-entity" role="button" aria-label={entityName(entity.id, naming)} aria-pressed={selectedId === entity.id} tabIndex={0} onPointerDown={(event) => event.stopPropagation()} onClick={() => setSelectedId(entity.id)} onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); setSelectedId(entity.id) } }}>
                <title>{entityName(entity.id, naming)} · {entity.id}</title>
                {isBuilding ? <rect className="entity-focus-ring" x="-15" y="-15" width="30" height="30" rx="6" /> : <circle className="entity-focus-ring" r="15" />}
                {isBuilding ? <rect x="-13" y="-13" width="26" height="26" rx="4" fill={color} /> : <circle r="13" fill={color} />}
                <g transform="translate(-10 -10) scale(0.8333)"><EntityGlyph type={entity.type} /></g>
                <rect x="-13" y="14" width="26" height="3" className="health-track" />
                <rect x="-13" y="14" style={{ width: healthWidth }} height="3" className="health-damage" aria-hidden="true" />
                <rect x="-13" y="14" style={{ width: healthWidth }} height="3" className="health-value" />
              </g></AttackNudge></MovingEntity>
            })}
          </g>
        </svg>
      </section>
      <div className="map-footer">
        <details className="map-key"><summary>Map legend</summary>
          <div className="map-legend"><span><i className="legend-ground" /> Ground</span><span><i className="legend-rock" /> Rock</span><span><i className="legend-forest" /> Forest · +3 troop defense</span><span><i className="legend-gold" /> Gold deposit · +5 economy income</span><span><i className="legend-build-area" /> Build area (faction tint)</span><span><i className="legend-movement" /> Movement (faction color)</span><span><i className="legend-attack" /> Attack (faction color)</span><span><i className="legend-healing" /> Treatment</span><span><i className="legend-tower" /> Tower fire</span></div>
          <EntityLegend types={entities.map((entity) => entity.type)} />
        </details>
        <EntityDetails selected={selected} naming={naming} onClear={() => {
          // Keep keyboard focus on the map: return it to the entity that was selected.
          entityNodes.current.get(selectedId ?? '')?.querySelector<SVGGElement>('.map-entity')?.focus()
          setSelectedId(null)
        }} />
      </div>
    </section>
  )
  return <>{!expanded && content}<dialog ref={dialog} className="expanded-map" aria-label="Expanded battlefield map" onKeyDown={trapFocus} onCancel={event => { event.preventDefault(); setExpanded(false) }} onClose={() => setExpanded(false)}>{expanded && content}</dialog></>
}

function EntityDetails({ selected, naming, onClear }: Readonly<{ selected?: Entity; naming: EntityNaming; onClear: () => void }>) {
  if (!selected) {
    return <div className="entity-details" aria-live="polite"><span>Select a castle, building, or unit for details.</span></div>
  }
  const targetIDs = [selected.attackTargetId, 'healTargetId' in selected ? selected.healTargetId : undefined]
    .filter(Boolean)
    .join(' · ')
  const moved = 'movedFrom' in selected && selected.movedFrom
    ? ` · moved from (${selected.movedFrom.x}, ${selected.movedFrom.y})`
    : ''
  const attacking = selected.attackTargetId ? ` · attacking ${entityName(selected.attackTargetId, naming)}` : ''
  const supportAction = 'repairAmount' in selected && selected.repairAmount ? 'repairing' : 'treating'
  const treating = 'healTargetId' in selected && selected.healTargetId
    ? ` · ${supportAction} ${entityName(selected.healTargetId, naming)}`
    : ''
  return <div className="entity-details" aria-live="polite">
    <strong title={selected.id}>{entityName(selected.id, naming)}</strong>
    <button className="entity-details__clear" type="button" aria-label="Clear selection" onClick={onClear}>×</button>
    {'income' in selected && <TrainingQueue building={selected} />}
    <span title={targetIDs}>HP {selected.health}/{selected.maxHealth} · ({selected.position.x}, {selected.position.y}){moved}{attacking}{treating} · {entityStats(selected)}</span>
  </div>
}

// Animate only recorded, continuous updates; opening a snapshot never replays a move.
function MovingEntity({ entity, tick, children, nodes }: Readonly<{ entity: Entity; tick: number; children: ReactNode; nodes: Map<string, SVGGElement> }>) {
  const element = useRef<SVGGElement>(null)
  const previous = useRef<{ tick: number; position: string } | null>(null)
  const position = key(entity.position)
  const center = centerOf(entity.position)
  const route = JSON.stringify('movedFrom' in entity ? movementRoute(entity) : [])
  useLayoutEffect(() => {
    const prior = previous.current
    previous.current = { tick, position }
    const points: HexPosition[] = JSON.parse(route)
    if (!prior || tick <= prior.tick || points.length < 2 || prior.position !== key(points[0])) return
    const node = element.current
    const preference = window.matchMedia?.('(prefers-reduced-motion: reduce)')
    if (!node?.animate || preference?.matches) return
    const animation = node.animate(points.map((point) => {
      const pixel = centerOf(point)
      return { transform: `translate(${pixel.x}px, ${pixel.y}px)` }
    }), { duration: Math.min(700, (points.length - 1) * 220), easing: 'linear' })
    const stop = () => { if (preference?.matches) animation.cancel() }
    preference?.addEventListener('change', stop)
    return () => {
      animation.cancel()
      preference?.removeEventListener('change', stop)
    }
  }, [tick, position, route])
  return <g ref={(node) => { element.current = node; if (node) nodes.set(entity.id, node); else nodes.delete(entity.id) }} transform={`translate(${center.x} ${center.y})`}>{children}</g>
}

function AttackNudge({ entity, tick, target, children }: Readonly<{ entity: Entity; tick: number; target?: Entity; children: ReactNode }>) {
  const node = useRef<SVGGElement>(null)
  const lastTick = useRef<number | null>(null)
  const destination = entity.attackTargetPosition && ('attackMode' in entity && entity.attackMode === 'position' ? entity.attackTargetPosition : target?.position ?? entity.attackTargetPosition)
  const from = centerOf(entity.position)
  const to = destination && centerOf(destination)
  const dx = to ? to.x - from.x : 0
  const dy = to ? to.y - from.y : 0
  const building = 'income' in entity
  useLayoutEffect(() => {
    if (lastTick.current === tick) return
    lastTick.current = tick
    const distance = Math.hypot(dx, dy)
    const preference = window.matchMedia?.('(prefers-reduced-motion: reduce)')
    if (building || !distance || !node.current?.animate || preference?.matches) return
    const animation = node.current.animate([
      { transform: 'translate(0px, 0px)' },
      { transform: `translate(${0.75 * dx / distance}px, ${0.75 * dy / distance}px)`, offset: .4 },
      { transform: 'translate(0px, 0px)' },
    ], { duration: 200, easing: 'ease-out' })
    const stop = () => { if (preference?.matches) animation.cancel() }
    preference?.addEventListener('change', stop)
    return () => { animation.cancel(); preference?.removeEventListener('change', stop) }
  }, [tick, dx, dy, building])
  return <g ref={node}>{children}</g>
}

function AttackVisual({ unit, target, entities, color, markerId, nodes, label }: Readonly<{
  unit: Entity; target?: Entity; entities: Entity[]; color: string; markerId: string; nodes: Map<string, SVGGElement>; label: string
}>) {
  const group = useRef<SVGGElement>(null)
  const fixed = 'attackMode' in unit && unit.attackMode === 'position'
  const destination = fixed ? unit.attackTargetPosition! : target?.position ?? unit.attackTargetPosition!
  const impact = fixed || !target
  const missed = unit.attackOutcome === 'miss'
  const line = actionLine(unit.position, destination, actionClearance(destination, unit.position, entities), impact ? 5 : actionClearance(unit.position, destination, entities))
  const kind = 'income' in unit ? 'tower' : 'attack'
  const center = centerOf(destination)
  const unitId = unit.id
  const targetId = fixed ? undefined : target?.id
  const coordinates = JSON.stringify([unit.position, destination])
  useLayoutEffect(() => {
    if (!line || !targetId || typeof requestAnimationFrame !== 'function') return
    let frame = 0
    let start: number | undefined
    // Read the same SVG transforms used by walking, including its intermediate frames.
    const update = (time: number) => {
      start ??= time
      const positions: HexPosition[] = JSON.parse(coordinates)
      const current = (id: string, fallback: HexPosition) => {
        const node = nodes.get(id)
        const matrix = node?.getCTM?.()
        const parent = (node?.parentNode as SVGGElement | undefined)?.getCTM?.()
        return matrix && parent ? { x: (matrix.e - parent.e) / parent.a, y: (matrix.f - parent.f) / parent.d } : centerOf(fallback)
      }
      const from = current(unitId, positions[0])
      const to = current(targetId, positions[1])
      const distance = Math.hypot(to.x - from.x, to.y - from.y)
      const clearance = (building: boolean) => building && distance > 0 ? Math.max(17, 17 * distance / Math.max(Math.abs(to.x - from.x), Math.abs(to.y - from.y))) : 17
      const startPadding = clearance(kind === 'tower')
      const endPadding = clearance(!!target && 'income' in target)
      const visible = distance > startPadding + endPadding + 4.5
      for (const element of group.current?.querySelectorAll('line') ?? []) {
        element.style.visibility = visible ? 'visible' : 'hidden'
        if (!visible) continue
        const dx = (to.x - from.x) / distance
        const dy = (to.y - from.y) / distance
        for (const [name, value] of Object.entries({ x1: from.x + dx * startPadding, y1: from.y + dy * startPadding, x2: to.x - dx * endPadding, y2: to.y - dy * endPadding })) element.setAttribute(name, String(value))
      }
      if (time - start < 750) frame = requestAnimationFrame(update)
    }
    frame = requestAnimationFrame(update)
    return () => cancelAnimationFrame(frame)
  }, [unitId, targetId, coordinates, nodes, !!line])
  return <g ref={group}>
    {line && <>
      <line className={`unit-direction unit-direction--attack ${kind === 'tower' ? 'unit-direction--tower' : ''} ${impact ? 'attack-impact-line' : ''}`} {...line} style={{ stroke: color }} markerEnd={`url(#${markerId}-${color.slice(1)})`}><title>{label}</title></line>
      {!missed && <line className={`unit-direction attack-tracer ${impact ? 'attack-impact-line' : ''}`} {...line} style={{ stroke: color }} pathLength="100" aria-hidden="true" />}
    </>}
    {impact && <g className={`attack-impact ${missed ? 'attack-impact--miss' : 'attack-impact--hit'}`} transform={`translate(${center.x} ${center.y})`}><title>{label} · {missed ? 'miss' : 'hit'}</title>{missed ? <path d="M-3,-3 L3,3 M-3,3 L3,-3" /> : <circle r="5" />}</g>}
  </g>
}

function movementRoute(unit: BattleUnit): HexPosition[] {
  if (unit.movementPath && unit.movementPath.length > 1 && key(unit.movementPath.at(-1)!) === key(unit.position)) return unit.movementPath
  if (!unit.movedFrom) return []
  const from = centerOf(unit.movedFrom)
  const to = centerOf(unit.position)
  const distance = Math.hypot(to.x - from.x, to.y - from.y)
  // Old snapshots only contain endpoints: animate an adjacent step, never invent a route.
  return distance > 0 && distance <= root3 * size + 0.01 ? [unit.movedFrom, unit.position] : []
}

export function centerOf(position: HexPosition) {
  return { x: root3 * size * (position.x + 0.5 * (position.y & 1)), y: size * 1.5 * position.y }
}
// Clear every icon at the endpoint, including square building selection outlines.
function actionClearance(fromPosition: HexPosition, position: HexPosition, entities: Entity[]) {
  const from = centerOf(fromPosition)
  const to = centerOf(position)
  const distance = Math.hypot(to.x - from.x, to.y - from.y)
  const hasBuilding = entities.some((entity) => 'income' in entity && key(entity.position) === key(position))
  return hasBuilding && distance > 0 ? Math.max(17, 17 * distance / Math.max(Math.abs(to.x - from.x), Math.abs(to.y - from.y))) : 17
}

function actionLine(from: HexPosition, to: HexPosition, startPadding: number, endPadding: number) {
  const start = centerOf(from)
  const end = centerOf(to)
  // Hide paths too short to fit a head rather than reversing or overlapping icons.
  if (Math.hypot(end.x - start.x, end.y - start.y) <= startPadding + endPadding + 4.5) return null
  return directionalLine(from, to, startPadding, endPadding)
}

function directionalLine(fromPosition: HexPosition, toPosition: HexPosition, startPadding: number, endPadding: number) {
  const from = centerOf(fromPosition)
  const to = centerOf(toPosition)
  const distance = Math.hypot(to.x - from.x, to.y - from.y) || 1
  const dx = (to.x - from.x) / distance
  const dy = (to.y - from.y) / distance
  return { x1: from.x + dx * startPadding, y1: from.y + dy * startPadding, x2: to.x - dx * endPadding, y2: to.y - dy * endPadding }
}
export function hexPoints(x: number, y: number) {
  return Array.from({ length: 6 }, (_, index) => {
    const angle = Math.PI / 180 * (60 * index - 30)
    return `${x + size * Math.cos(angle)},${y + size * Math.sin(angle)}`
  }).join(' ')
}
function key(position: HexPosition) { return `${position.x}:${position.y}` }
export function terrainClass(symbol: string) {
  if (symbol === '#') return 'rock'
  if (symbol === 'f') return 'forest'
  if (symbol === 'g') return 'gold'
  return 'ground'
}

function entityStats(entity: Entity) {
  const stats = [`Defense ${entity.defense}`]
  if ('income' in entity) stats.push(`Build range ${entity.buildRange || 3}`, `Income ${entity.income}/tick`)
  const minimumRange = 'minimumRange' in entity && entity.minimumRange ? `${entity.minimumRange}–` : ''
  if (entity.attack) stats.push(`Attack ${entity.attack}`, `Range ${minimumRange}${entity.range}`)
  stats.push(...specialistStats(entity))
  if ('buildingDamageBonus' in entity && entity.buildingDamageBonus) stats.push(`+${entity.buildingDamageBonus} vs buildings`)
  if ('classDamageBonus' in entity && entity.classDamageBonus) stats.push(`+${entity.classDamageBonus} vs ${entity.bonusAgainstClass}`)
  if ('trains' in entity && entity.trains?.length) stats.push(`Trains: ${entity.trains.map(typeLabel).join(', ')}`)
  return stats.join(' · ')
}

function specialistStats(entity: Entity) {
  const stats: string[] = []
  if ('healAmount' in entity && entity.healAmount) stats.push(`Treat ${entity.healAmount} HP · range ${entity.healRange || 1}`)
  if ('repairAmount' in entity && entity.repairAmount) stats.push(`Repair ${entity.repairAmount} HP · ${entity.repairCost} gold · ${entity.repairCharges ?? 0} charges`)
  if ('armorPiercing' in entity && entity.armorPiercing) stats.push(`Ignore ${entity.armorPiercing} unit defense`)
  if ('scout' in entity && entity.scout) stats.push('Skirmisher · retreats from nearby enemies')
  if ('preferredTargetClass' in entity && entity.preferredTargetClass) stats.push(`Prefers ${entity.preferredTargetClass} targets`)
  return stats
}

export function terrainDescription(symbol: string) {
  if (symbol === 'f') return 'Forest: +3 troop defense; no construction'
  if (symbol === 'g') return 'Gold deposit: economy buildings earn +5 gold per tick'
  if (symbol === '#') return 'Rock: impassable'
  return 'Ground: build within friendly range, leaving one hex between buildings'
}

// Where each faction may build now: buildable terrain within range of one of its active
// buildings and at least two hexes from every living building, as the engine requires.
// Units also block a site, but they move every tick, so the tint ignores them.
function buildAreas(map: BattleMap, state: BattleState) {
  const buildings = (state.buildings ?? []).filter((building) => building.health > 0)
  const areas = new Map<string, HexPosition[]>()
  map.rows.forEach((row, y) => [...row].forEach((symbol, x) => {
    const position = { x, y }
    if (!'.gb'.includes(symbol) || buildings.some((building) => hexDistance(building.position, position) <= 1)) return
    const factions = new Set(buildings
      .filter((building) => (building.readyAtTick ?? 0) <= state.tick && hexDistance(building.position, position) <= (building.buildRange || 3))
      .map((building) => building.factionId))
    for (const factionId of factions) areas.set(factionId, [...(areas.get(factionId) ?? []), position])
  }))
  return areas
}

function hexDistance(a: HexPosition, b: HexPosition) {
  const aq = a.x - (a.y - (a.y & 1)) / 2
  const bq = b.x - (b.y - (b.y & 1)) / 2
  return (Math.abs(aq - bq) + Math.abs(aq + a.y - bq - b.y) + Math.abs(a.y - b.y)) / 2
}
