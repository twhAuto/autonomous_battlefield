import { type PointerEvent, type ReactNode, useMemo, useState } from 'react'
import type { BattleMap, BattleState, HexPosition } from '../api/battles'

type Entity = BattleState['units'][number] | BattleState['buildings'][number]
const size = 24
const root3 = Math.sqrt(3)

export function HexBattleMap({ map, state, visiblePositions, factionNames, toolbarControls }: Readonly<{
  map: BattleMap
  state: BattleState
  visiblePositions: HexPosition[]
  factionNames: Map<string, string>
  toolbarControls?: ReactNode
}>) {
  const [scale, setScale] = useState(1)
  const [pan, setPan] = useState({ x: 24, y: 30 })
  const [drag, setDrag] = useState<{ x: number; y: number } | null>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const visible = useMemo(() => new Set(visiblePositions.map(key)), [visiblePositions])
  const liveUnits = (state.units ?? []).filter((unit) => unit.health > 0)
  const entities: Entity[] = [...(state.buildings ?? []), ...liveUnits]
  const selected = entities.find((entity) => entity.id === selectedId)
  const colors = new Map(state.factions?.map((faction, index) => [faction.factionId, index === 0 ? '#69d2ff' : '#ff6b55']))
  const width = root3 * size * (map.width + 0.5) + 48
  const height = size * (1.5 * (map.height - 1) + 2) + 48

  function pointerMove(event: PointerEvent<SVGSVGElement>) {
    if (!drag) return
    setPan((current) => ({ x: current.x + event.clientX - drag.x, y: current.y + event.clientY - drag.y }))
    setDrag({ x: event.clientX, y: event.clientY })
  }
  function zoom(delta: number) { setScale((current) => Math.max(0.55, Math.min(2.4, current + delta))) }

  return (
    <section className="map-panel" aria-label="Castle siege map">
      <div className="map-toolbar">
        <span>HEX TACTICAL MAP · TICK {state.tick}</span>
        <div className="map-toolbar__controls">
          {toolbarControls}
          <div className="map-zoom-controls"><button type="button" onClick={() => zoom(-0.15)} aria-label="Zoom out">−</button><button type="button" onClick={() => zoom(0.15)} aria-label="Zoom in">+</button><button type="button" onClick={() => { setScale(1); setPan({ x: 24, y: 30 }) }}>RESET</button></div>
        </div>
      </div>
      <div className="map-viewport">
        <svg
          viewBox={`0 0 ${width} ${height}`}
          onPointerDown={(event) => { event.currentTarget.setPointerCapture?.(event.pointerId); setDrag({ x: event.clientX, y: event.clientY }) }}
          onPointerMove={pointerMove}
          onPointerUp={() => setDrag(null)}
          onPointerCancel={() => setDrag(null)}
        >
          <title>{map.width} by {map.height} pointy-top hex battlefield</title>
          <defs>
            <marker id="movement-arrowhead" markerWidth="7" markerHeight="7" refX="6" refY="3.5" orient="auto"><path d="M0,0 L7,3.5 L0,7 Z" /></marker>
            <marker id="attack-arrowhead" markerWidth="7" markerHeight="7" refX="6" refY="3.5" orient="auto"><path d="M0,0 L7,3.5 L0,7 Z" /></marker>
          </defs>
          <g className="map-layer" transform={`translate(${pan.x} ${pan.y}) scale(${scale})`}>
            {map.rows.map((row, y) => [...row].map((symbol, x) => {
              const position = { x, y }
              const center = centerOf(position)
              const isVisible = visible.has(key(position))
              return <polygon key={`${x}:${y}`} points={hexPoints(center.x, center.y)} className={`hex-tile hex-tile--${terrainClass(symbol)} ${isVisible ? '' : 'hex-tile--fog'}`} />
            }))}
            {liveUnits.filter((unit) => unit.movedFrom).map((unit) => {
              const line = directionalLine(unit.movedFrom!, unit.position, 2, 14)
              return <line key={`move:${unit.id}`} className="unit-direction unit-direction--movement" {...line} markerEnd="url(#movement-arrowhead)"><title>{unit.id} moved from {unit.movedFrom!.x}, {unit.movedFrom!.y} to {unit.position.x}, {unit.position.y}</title></line>
            })}
            {liveUnits.filter((unit) => unit.attackTargetPosition).map((unit) => {
              const line = directionalLine(unit.position, unit.attackTargetPosition!, 12, 14)
              return <line key={`attack:${unit.id}`} className="unit-direction unit-direction--attack" {...line} markerEnd="url(#attack-arrowhead)"><title>{unit.id} attacked {unit.attackTargetId}</title></line>
            })}
            {entities.map((entity) => {
              const center = centerOf(entity.position)
              const isBuilding = 'income' in entity
              const color = colors.get(entity.factionId) ?? '#f2c94c'
              return <g key={entity.id} className="map-entity" role="button" aria-label={`${entity.type} ${entity.id}`} tabIndex={0} transform={`translate(${center.x} ${center.y})`} onPointerDown={(event) => event.stopPropagation()} onClick={() => setSelectedId(entity.id)} onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') setSelectedId(entity.id) }}>
                {isBuilding ? <path d={entity.type === 'castle' ? 'M-12 11V-8L-7-4V-12L0-7L7-12V-4L12-8V11Z' : 'M-11 10V-6L0-13L11-6V10Z'} fill={color} /> : <circle r="10" fill={color} />}
                {!isBuilding && <text textAnchor="middle" y="4">{entity.type.slice(0, 1).toUpperCase()}</text>}
                <rect x="-13" y="14" width="26" height="3" className="health-track" /><rect x="-13" y="14" width={26 * Math.max(0, entity.health / entity.maxHealth)} height="3" className="health-value" />
              </g>
            })}
          </g>
        </svg>
      </div>
      <div className="map-legend"><span><i className="legend-ground" /> Ground</span><span><i className="legend-rock" /> Rock</span><span><i className="legend-site" /> Build site</span><span><i className="legend-fog" /> Outside vision</span><span><i className="legend-movement" /> Movement</span><span><i className="legend-attack" /> Attack</span></div>
      <div className="entity-details" aria-live="polite">
        {selected ? <><strong>{selected.type} · {factionNames.get(selected.factionId) ?? selected.factionId}</strong><span>HP {selected.health}/{selected.maxHealth} · ({selected.position.x}, {selected.position.y}){'movedFrom' in selected && selected.movedFrom ? ` · moved from (${selected.movedFrom.x}, ${selected.movedFrom.y})` : ''}{'attackTargetId' in selected && selected.attackTargetId ? ` · attacking ${selected.attackTargetId}` : ''}</span></> : <span>Select a castle, building, or unit for details.</span>}
      </div>
    </section>
  )
}

function centerOf(position: HexPosition) {
  return { x: root3 * size * (position.x + 0.5 * (position.y & 1)), y: size * 1.5 * position.y }
}
function directionalLine(fromPosition: HexPosition, toPosition: HexPosition, startPadding: number, endPadding: number) {
  const from = centerOf(fromPosition)
  const to = centerOf(toPosition)
  const distance = Math.hypot(to.x - from.x, to.y - from.y) || 1
  const dx = (to.x - from.x) / distance
  const dy = (to.y - from.y) / distance
  return { x1: from.x + dx * startPadding, y1: from.y + dy * startPadding, x2: to.x - dx * endPadding, y2: to.y - dy * endPadding }
}
function hexPoints(x: number, y: number) {
  return Array.from({ length: 6 }, (_, index) => {
    const angle = Math.PI / 180 * (60 * index - 30)
    return `${x + size * Math.cos(angle)},${y + size * Math.sin(angle)}`
  }).join(' ')
}
function key(position: HexPosition) { return `${position.x}:${position.y}` }
function terrainClass(symbol: string) {
  if (symbol === '#') return 'rock'
  if (symbol === 'b') return 'site'
  return 'ground'
}
