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
  expect(within(nav).getAllByRole('link').map(link => link.textContent)).toEqual(['Stage battle', 'Factions', 'Battle history', 'Leagues', 'Statistics', 'Admin'])
  await user.tab()
  await user.keyboard('{Escape}')
  expect(menu).toHaveFocus()
  expect(menu).toHaveAttribute('aria-expanded', 'false')
  await user.click(menu)
  await user.click(within(screen.getByRole('navigation')).getByRole('button', { name: 'Log out admin' }))
  expect(authentication.logout).toHaveBeenCalledOnce()
  expect(await screen.findByRole('heading', { name: 'Identify commander.' })).toBeInTheDocument()
})

it('shows leagues and statistics to every role', () => {
  vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener() {}, removeEventListener() {} }))
  const authentication = { session: vi.fn(), login: vi.fn(), logout: vi.fn() }
  const links = (role: 'guest' | 'player' | 'tester') => {
    const view = render(<MemoryRouter><App authentication={authentication} initialSession={{ enabled: true, authenticated: true, mode: 'cloudflare', actor: { id: role, role } }} /></MemoryRouter>)
    const names = within(screen.getByRole('navigation')).getAllByRole('link').map(link => link.textContent)
    view.unmount()
    return names
  }
  expect(links('guest')).toEqual(['Factions', 'Battle history', 'Leagues', 'Statistics'])
  expect(links('player')).toEqual(['Stage battle', 'Factions', 'Battle history', 'Leagues', 'Statistics'])
  expect(links('tester')).toEqual(['Stage battle', 'Factions', 'Battle history', 'Leagues', 'Statistics'])
})

it('shows not found to a Tester opening the league setup route', () => {
  vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener() {}, removeEventListener() {} }))
  vi.stubGlobal('fetch', vi.fn())
  const authentication = { session: vi.fn(), login: vi.fn(), logout: vi.fn() }
  render(<MemoryRouter initialEntries={['/leagues/new']}><App authentication={authentication} initialSession={{ enabled: true, authenticated: true, mode: 'cloudflare', actor: { id: 'tester', role: 'tester' } }} /></MemoryRouter>)
  expect(screen.getByRole('heading', { name: 'This route is outside the mapped frontier.' })).toBeInTheDocument()
  expect(vi.mocked(fetch).mock.calls.map(([input]) => String(input)).filter(url => url.includes('/v1/leagues/new'))).toEqual([])
})
