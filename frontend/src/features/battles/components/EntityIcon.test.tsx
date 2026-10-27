import { render } from '@testing-library/react'
import { expect, it } from 'vitest'
import { EntityIcon } from './EntityIcon'

const artwork = (type: string) => {
  const { container, unmount } = render(<EntityIcon type={type} />)
  const path = container.querySelector('path')?.getAttribute('d')
  unmount()
  return path
}

it('draws distinct artwork for late-game units', () => {
  const unknown = artwork('not-a-unit')
  const drawn = ['cannon', 'gunner', 'heavy_cavalry', 'raider_cavalry'].map(artwork)
  expect(drawn).not.toContain(unknown)
  expect(new Set(drawn).size).toBe(drawn.length)
})
