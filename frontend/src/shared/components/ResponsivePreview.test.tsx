import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { ResponsivePreview } from './ResponsivePreview'

afterEach(() => vi.unstubAllGlobals())
it('collapses mobile previews and keeps them available on desktop resize', async () => {
  let change = () => {}
  const query = { matches: true, addEventListener: vi.fn((_type, cb) => { change = cb }), removeEventListener: vi.fn() }
  vi.stubGlobal('matchMedia', () => query)
  const { unmount } = render(<ResponsivePreview label="Preview faction"><p>Identity preview</p></ResponsivePreview>)
  expect(screen.getByText('Identity preview')).not.toBeVisible()
  await userEvent.click(screen.getByText('Preview faction'))
  expect(screen.getByText('Identity preview')).toBeVisible()
  act(() => { query.matches = false; change() })
  expect(screen.getByText('Identity preview')).toBeVisible()
  unmount()
  expect(query.removeEventListener).toHaveBeenCalled()
})
