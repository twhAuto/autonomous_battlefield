// Draws a static terrain layout of a scenario map with both castle spawns, for battle setup.

import type { BattleMap } from '../api/battles'
import { centerOf, hexPoints, terrainClass, terrainDescription } from './HexBattleMap'
import { TerrainGlyph } from './TerrainGlyph'
import { factionColors, unknownFactionColor } from '../presentation/factions'

const size = 24
const root3 = Math.sqrt(3)

// Castle colors match the live map, which assigns them by sorted faction ID rather than slot.
export function MapPreview({ map, scenarioId, factionIds }: Readonly<{ map: BattleMap; scenarioId: string; factionIds: [string, string] }>) {
  const width = root3 * size * (map.width + 0.5)
  const height = size * (1.5 * (map.height - 1) + 2)
  const colors = factionColors(factionIds)
  return (
    <figure className="map-preview">
      <svg viewBox={`${-root3 * size / 2} ${-size} ${width} ${height}`}>
        <title>{`${scenarioId} layout, ${map.width} by ${map.height} hexes`}</title>
        {map.rows.map((row, y) => [...row].map((symbol, x) => {
          const center = centerOf({ x, y })
          return <g key={`${x}:${y}`}>
            <polygon points={hexPoints(center.x, center.y)} className={`hex-tile hex-tile--${terrainClass(symbol)}`}><title>{terrainDescription(symbol)} · ({x}, {y})</title></polygon>
            <g transform={`translate(${center.x - 12} ${center.y - 12})`}><TerrainGlyph symbol={symbol} /></g>
          </g>
        }))}
        {map.castleSpawns.map((spawn, slot) => {
          const center = centerOf(spawn)
          const color = colors.get(factionIds[slot]) ?? unknownFactionColor
          return <g key={`castle:${spawn.x}:${spawn.y}`} className="map-preview__castle" transform={`translate(${center.x} ${center.y})`} style={{ stroke: color, fill: color }}>
            <title>Slot {slot + 1} castle · ({spawn.x}, {spawn.y})</title>
            <circle r="15" />
            <text dy="5" textAnchor="middle">{slot + 1}</text>
          </g>
        })}
      </svg>
      <figcaption>
        {['castle', 'gold', 'forest', 'rock'].map(kind => <span key={kind}><i className={`map-preview__key map-preview__key--${kind}`} aria-hidden="true" />{kind}</span>)}
      </figcaption>
    </figure>
  )
}
