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
