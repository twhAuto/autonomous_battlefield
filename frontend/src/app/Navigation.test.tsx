import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'
import App from './App'

afterEach(() => vi.unstubAllGlobals())
it('exposes mobile navigation and logout, closes on Escape and restores focus', async () => {
  vi.stubGlobal('matchMedia', () => ({ matches: true, addEventListener() {}, removeEventListener() {} }))
  const authentication = { session: vi.fn(), login: vi.fn(), logout: vi.fn().mockResolvedValue(undefined) }
  render(<MemoryRouter><App authentication={authentication} initialSession={{ enabled: true, authenticated: true, actor: { id: 'admin' } }} /></MemoryRouter>)
  const user = userEvent.setup()
  const menu = screen.getByRole('button', { name: 'Menu' })
  expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
  await user.click(menu)
  expect(menu).toHaveAttribute('aria-expanded', 'true')
  const nav = screen.getByRole('navigation')
  expect(within(nav).getAllByRole('link')).toHaveLength(4)
  await user.tab()
  await user.keyboard('{Escape}')
  expect(menu).toHaveFocus()
  expect(menu).toHaveAttribute('aria-expanded', 'false')
  await user.click(menu)
  await user.click(within(screen.getByRole('navigation')).getByRole('button', { name: 'Log out admin' }))
  expect(authentication.logout).toHaveBeenCalledOnce()
  expect(await screen.findByRole('heading', { name: 'Identify commander.' })).toBeInTheDocument()
})
