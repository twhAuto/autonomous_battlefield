// Checks emblem palette selection uses complete Unicode code points from its seed.

import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Emblem } from './Emblem'

describe('Emblem', () => {
  it('uses the full Unicode code point when selecting a palette', () => {
    const { container } = render(<Emblem seed="😀" />)
    const emblem = container.firstElementChild as HTMLElement

    expect(emblem.style.getPropertyValue('--emblem-primary')).toBe('#d8ff68')
    expect(emblem.style.getPropertyValue('--emblem-secondary')).toBe('#58804b')
  })
})

it('draws distinct artwork for each supported emblem and falls back safely', () => {
 const { container, rerender } = render(<Emblem />)
 const star = container.querySelector('.emblem__mark')!.innerHTML
 const symbols = new Set<string>()
 for (const emblem of ['star', 'crown', 'shield', 'swords', 'wolf', 'raven', 'serpent', 'tree', 'leaf', 'flame', 'sun', 'moon']) {
  rerender(<Emblem emblem={emblem} />)
  symbols.add(container.querySelector('.emblem__mark')!.innerHTML)
 }
 expect(symbols.size).toBe(12)
 rerender(<Emblem emblem="unknown" />)
 expect(container.querySelector('.emblem__mark')!.innerHTML).toBe(star)
})
