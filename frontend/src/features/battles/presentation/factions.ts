// Faction colors shared by the map and faction strips, so a strip matches its units.
const palette = ['#69d2ff', '#ff6b55']
export const unknownFactionColor = '#f2c94c'

// Colors follow the participant roster sorted by ID, independent of entity order.
// Code-unit order, not localeCompare: it must match the backend's byte-order ID sort.
export function factionColors(roster: Iterable<string>) {
  const sorted = [...roster].sort((a, b) => (a < b ? -1 : Number(a > b)))
  return new Map(sorted.map((factionId, index) => [factionId, palette[Math.min(index, palette.length - 1)]]))
}
