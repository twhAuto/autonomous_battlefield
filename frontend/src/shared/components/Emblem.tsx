// Reusable decorative faction emblem with a deterministic color palette derived from its
// text seed.

type EmblemProps = Readonly<{
  seed?: string
  size?: 'large' | 'small'
}>

const palettes = [
  ['#d8ff68', '#58804b'],
  ['#ffc96b', '#995c3d'],
  ['#78e8ff', '#456999'],
  ['#ef8cff', '#754b91'],
]

function paletteFor(seed: string) {
  const total = [...seed].reduce((sum, character) => sum + character.codePointAt(0)!, 0)
  return palettes[total % palettes.length]
}

export function Emblem({ seed = '', size = 'large' }: EmblemProps) {
  const [primary, secondary] = paletteFor(seed)

  return (
    <div
      className={`emblem emblem--${size}`}
      style={{ '--emblem-primary': primary, '--emblem-secondary': secondary } as React.CSSProperties}
      aria-hidden="true"
    >
      <svg viewBox="0 0 120 120">
        <path className="emblem__frame" d="M60 5 108 31v58l-48 26L12 89V31L60 5Z" />
        <path className="emblem__field" d="M60 15 99 36v47l-39 22-39-22V36l39-21Z" />
        <path className="emblem__mark" d="m60 28 8 19 20 2-15 14 5 21-18-10-18 10 5-21-15-14 20-2 8-19Z" />
        <circle className="emblem__core" cx="60" cy="59" r="8" />
      </svg>
    </div>
  )
}
