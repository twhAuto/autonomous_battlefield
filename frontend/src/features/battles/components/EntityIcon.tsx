// Local, shared 24px tactical artwork for map markers, rosters, and the legend.
import { typeLabel } from '../presentation/entities'

const artwork = new Map<string, string>([
  ['vanguard', 'M12 2 21 6V12C21 17 16 21 12 23 8 21 3 17 3 12V6ZM12 6V18M7 9H17'],
  ['striker', 'M3 2 9 5 20 19 18 21 4 7ZM21 2 15 5 4 19 6 21 20 7ZM2 16 8 22M16 22 22 16'],
  ['ranger', 'M6 2C23 5 23 19 6 22L12 12ZM3 12H23M19 8 23 12 19 16'],
  ['catapult', 'M3 17H21M7 17 11 8 17 17M9 13 18 3M15 3H22L20 7ZM7 20A2 2 0 1 0 3 20 2 2 0 1 0 7 20M21 20A2 2 0 1 0 17 20 2 2 0 1 0 21 20'],
  ['battering_ram', 'M3 9 7 3H17L21 9ZM6 9V17M18 9V17M2 12H20L23 14 20 16H2ZM7 21A2 2 0 1 0 3 21 2 2 0 1 0 7 21M21 21A2 2 0 1 0 17 21 2 2 0 1 0 21 21'],
  ['pikeman', 'M4 22 16 8M14 8 21 2 19 11ZM9 13 13 17M3 5 8 3 12 5V10L8 14 3 10Z'],
  ['herbalist', 'M3 20C-1 7 8 3 22 2 22 16 17 22 6 20M2 23 17 7M7 17V10M11 13H17'],
  ['scout', 'M2 12 7 7H17L22 12 17 17H7ZM15 12A3 3 0 1 0 9 12 3 3 0 1 0 15 12M12 2V5M12 19V22'],
  ['raider_cavalry', 'M4 21V13L9 8 8 3 13 5 18 3 21 10 17 13H13L16 21M3 21H19'],
  ['engineer', 'M15 2A6 6 0 0 0 10 11L2 19 5 22 13 14A6 6 0 0 0 22 8L17 11 13 7Z'],
  ['crossbowman', 'M3 7Q12 1 21 7M3 7 12 12 21 7M12 2V21M8 21H16M9 6 12 2 15 6'],
  ['castle', 'M2 22V3H6V7H10V3H14V7H18V3H22V22ZM9 22V15A3 3 0 0 1 15 15V22'],
  ['economy', 'M2 10 10 3 18 10M4 9V21H9M7 12H10M22 16A6 6 0 1 0 10 16 6 6 0 1 0 22 16M16 12V20M18 13H15A1.5 1.5 0 0 0 15 16H17A1.5 1.5 0 0 1 17 19H14'],
  ['defense_tower', 'M5 22 7 9H17L19 22ZM5 9V2H9V5H15V2H19V9ZM10 13H20L23 10M11 18H14'],
  ['outpost', 'M5 22 8 11M19 22 16 11M7 17H17M5 11V5H19V11ZM2 5 12 1 22 5M10 7H14'],
  ['watchtower', 'M5 22 8 11M19 22 16 11M7 17H17M5 11V5H19V11ZM2 5 12 1 22 5M10 7H14'],
  ['archery_range', 'M2 22V10L12 2 22 10V22ZM12 10A5 5 0 1 0 12 20 5 5 0 1 0 12 10M12 13V17M10 15H14'],
  ['stables', 'M2 22V10L12 2 22 10V22ZM8 21V16L12 11 12 7 17 10 19 15 15 17 17 21'],
  ['siege_workshop', 'M2 22V9L9 4V9L16 4V9H22V22ZM7 18 15 10M12 8 18 14M5 16 9 20'],
  ['infirmary', 'M2 22V10L12 2 22 10V22ZM10 11H14V14H17V18H14V21H10V18H7V14H10Z'],
  ['barracks', 'M2 22 12 3 22 22ZM8 22 12 13 16 22M12 3V1H20L17 4H13'],
])
const unknownArtwork = 'M12 2 22 12 12 22 2 12ZM12 7V13M12 17H12.01'

export function EntityGlyph({ type }: Readonly<{ type: string }>) {
  return <g className="entity-glyph" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d={artwork.get(type) ?? unknownArtwork} /></g>
}

export function EntityIcon({ type }: Readonly<{ type: string }>) {
  return <svg className="entity-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><EntityGlyph type={type} /></svg>
}

export function EntityLegend({ types }: Readonly<{ types: string[] }>) {
  return <details className="entity-legend"><summary>Unit &amp; building icons</summary>
    <div>{[...new Set(types)].sort((a, b) => a.localeCompare(b)).map((type) => <span key={type}><EntityIcon type={type} />{typeLabel(type)}</span>)}</div>
  </details>
}
