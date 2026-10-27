// Reusable decorative faction emblem with a deterministic color palette derived from its
// text seed, with a saved symbol and a safe default for legacy or unknown identifiers.
import { resolveEmblem } from '../domain/emblems'

type EmblemProps = Readonly<{
  seed?: string
  emblem?: string
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

export function Emblem({ seed = '', size = 'large', emblem }: EmblemProps) {
  const [primary, secondary] = paletteFor(seed)

  return (
    <div
      className={`emblem emblem--${size}`}
      style={{ '--emblem-primary': primary, '--emblem-secondary': secondary } as React.CSSProperties}
      aria-hidden="true"
      data-emblem={resolveEmblem(emblem)}
    >
      <svg viewBox="0 0 120 120">
        <path className="emblem__frame" d="M60 5 108 31v58l-48 26L12 89V31L60 5Z" />
        <path className="emblem__field" d="M60 15 99 36v47l-39 22-39-22V36l39-21Z" />
        <g className="emblem__mark" strokeLinejoin="round" strokeLinecap="round">
          <EmblemMark emblem={resolveEmblem(emblem)} />
        </g>
      </svg>
    </div>
  )
}

function EmblemMark({ emblem }: Readonly<{ emblem: string }>) {
  switch (emblem) {
    case 'crown': return <><path d="m32 42 14 12 14-23 14 23 14-12-6 36H38Z" /><path className="emblem__detail" d="M39 86h42M46 67h28" /><circle className="emblem__core" cx="60" cy="61" r="4" /></>
    case 'shield': return <><path d="m60 29 27 10v22c0 16-17 27-27 32-10-5-27-16-27-32V39Z" /><path className="emblem__detail" d="M60 39v42M42 54h36" /></>
    case 'swords': return <><path d="m34 29 12 4 37 45-7 6-37-44Zm52 0-12 4-37 45 7 6 37-44Z" /><path className="emblem__detail" d="m30 70 20 17m20-1 20-17M39 81l-7 10m48-10 8 10" /></>
    case 'wolf': return <><path d="m34 30 21 14h10l21-14-3 34-10 20-13 9-13-9-10-20Z" /><path className="emblem__detail" d="m42 58 10 5m26-5-10 5M47 76l13-6 13 6" /><path className="emblem__core" d="m54 77 6 7 6-7Z" /></>
    case 'raven': return <><path d="m30 73 13-27 20-12 14 8 13 13-17-2 1 20-17 17 3-21-14 11 2-17Z" /><path className="emblem__detail" d="m44 59 18-12-5 17" /><circle className="emblem__core" cx="71" cy="44" r="2" /></>
    case 'serpent': return <><path d="m72 31 14 10-14 10-9-4c-15-5-20 6-8 11l14 5c25 10 12 31-10 28L36 81l22 2c13 0 17-8 4-13l-13-5c-26-10-15-33 7-31l7 3Z" /><circle className="emblem__core" cx="73" cy="40" r="2" /></>
    case 'tree': return <><path d="m60 27 20 23H70l17 20H68v17H52V70H33l17-20H40Z" /><path className="emblem__detail" d="M45 89h30M60 47v40" /></>
    case 'leaf': return <><path d="M88 30c-43-4-65 16-48 43 27 17 47-5 48-43Z" /><path className="emblem__detail" d="m32 89 43-45M50 71V54m10 7h16" /></>
    case 'flame': return <><path d="M61 27c5 22 24 22 24 42a25 25 0 0 1-50 0c0-11 7-20 14-27-2 14 3 17 5 18 9-10 2-23 7-33Z" /><path className="emblem__core" d="M60 61c3 10 13 15 8 23-6 8-18 2-15-6 2-6 5-9 7-17Z" /></>
    case 'sun': return <><circle cx="60" cy="60" r="19" /><path className="emblem__detail" d="M60 27v8m0 50v8M27 60h8m50 0h8M37 37l6 6m34 34 6 6M37 83l6-6m34-34 6-6" /><circle className="emblem__core" cx="60" cy="60" r="7" /></>
    case 'moon': return <><path d="M70 29a32 32 0 1 0 20 43A28 28 0 0 1 70 29Z" /><path className="emblem__core" d="m81 33 3 9 9 3-9 3-3 9-3-9-9-3 9-3Z" /></>
    default: return <><path d="m60 28 8 19 20 2-15 14 5 21-18-10-18 10 5-21-15-14 20-2 8-19Z" /><circle className="emblem__core" cx="60" cy="59" r="8" /></>
  }
}
