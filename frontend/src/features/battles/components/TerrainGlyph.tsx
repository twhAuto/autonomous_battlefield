// Tile-local 24px silhouettes stay recognizable when the battlefield is zoomed out.
export function TerrainGlyph({ symbol }: Readonly<{ symbol: string }>) {
  if (symbol === 'f') return <g className="terrain-glyph terrain-glyph--forest" aria-hidden="true"><path d="M8 2 2 11H5L1 17H15L11 11H14ZM17 6 12 14H15L12 19H23L20 14H22ZM8 17V22M18 19V23" /></g>
  if (symbol === 'g') return <g className="terrain-glyph terrain-glyph--gold" aria-hidden="true"><path d="M2 16 6 10 13 12 15 19 8 22ZM12 10 16 3 21 6 23 14 18 17ZM6 10 8 16 15 19M16 3 17 10 23 14M8 16 8 22" /></g>
  if (symbol === '#') return <g className="terrain-glyph terrain-glyph--rock" aria-hidden="true"><path d="M2 19 4 10 12 4 19 8 23 19ZM4 10 13 12 19 8M13 12 15 19" /></g>
  return null
}
