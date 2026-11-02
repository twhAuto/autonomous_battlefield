// Covers the first-sign-in display-name prompt, the signed-in label in the top bar and
// mobile menu, and changing the name later from the profile page.

import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import App from './App'
import type { AuthSession } from '../features/auth/api/auth'

const authentication = { session: vi.fn(), login: vi.fn(), logout: vi.fn() }
const session = (role: 'guest' | 'player' | 'tester' | 'admin', displayName?: string): AuthSession => ({
  enabled: true, authenticated: true, mode: 'cloudflare', actor: { id: `issuer|${role}`, email: `${role}@example.com`, role, displayName },
})
const saved = (displayName: string) => new Response(JSON.stringify({ displayName }), { status: 200 })

function renderApp(initialSession: AuthSession, path = '/') {
  return render(<MemoryRouter initialEntries={[path]}><App authentication={authentication} initialSession={initialSession} /></MemoryRouter>)
}

function setMobile(matches: boolean) {
  vi.stubGlobal('matchMedia', () => ({ matches, addEventListener() {}, removeEventListener() {} }))
}

beforeEach(() => {
  setMobile(false)
  vi.stubGlobal('fetch', vi.fn())
  authentication.logout.mockReset().mockResolvedValue(undefined)
})
afterEach(() => vi.unstubAllGlobals())

it.each(['player', 'tester'] as const)('asks a %s without a name to choose one before entering', async (role) => {
  vi.mocked(fetch).mockResolvedValueOnce(new Response('{"error":{"code":"conflict","message":"taken"}}', { status: 409 })).mockResolvedValueOnce(saved('Iron Duke'))
  const user = userEvent.setup()
  renderApp(session(role))

  expect(screen.getByRole('heading', { name: 'Choose your display name' })).toBeInTheDocument()
  expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
  const name = screen.getByLabelText('Display name')
  expect(name).toHaveAccessibleDescription(/3 to 24 letters/)

  await user.type(name, 'A!')
  await user.click(screen.getByRole('button', { name: /ENTER COMMAND POST/ }))
  expect(screen.getByRole('alert')).toHaveTextContent('Use 3 to 24 letters, digits, spaces, hyphens or underscores.')
  expect(name).toHaveAttribute('aria-invalid', 'true')
  expect(fetch).not.toHaveBeenCalled()

  await user.clear(name)
  await user.type(name, '  Iron Duke ')
  await user.click(screen.getByRole('button', { name: /ENTER COMMAND POST/ }))
  expect(await screen.findByRole('alert')).toHaveTextContent('That name is taken. Choose another.')

  await user.click(screen.getByRole('button', { name: /ENTER COMMAND POST/ }))
  expect(await screen.findByRole('heading', { name: 'Main menu' })).toBeInTheDocument()
  expect(fetch).toHaveBeenLastCalledWith('/v1/profile', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ displayName: 'Iron Duke' }) }))
  expect(screen.getByRole('link', { name: 'Signed in as Iron Duke' })).toHaveAttribute('href', '/profile')
})

it('lets the prompt sign out instead', async () => {
  const user = userEvent.setup()
  renderApp(session('player'))
  await user.click(screen.getByRole('button', { name: 'LOG OUT' }))
  expect(authentication.logout).toHaveBeenCalledOnce()
  expect(await screen.findByRole('heading', { name: 'Sign in to Battlefield' })).toBeInTheDocument()
})

it('keeps the prompt when sign-out cannot be confirmed', async () => {
  authentication.logout.mockRejectedValue(new Error('offline'))
  const user = userEvent.setup()
  renderApp(session('tester'))
  await user.click(screen.getByRole('button', { name: 'LOG OUT' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Logout could not be confirmed.')
  expect(screen.getByRole('heading', { name: 'Choose your display name' })).toBeInTheDocument()
})

it.each([
  ['admin', 'Admin'],
  ['guest', 'Guest'],
] as const)('never asks an %s and signs them in as %s', (role, label) => {
  renderApp(session(role))
  expect(screen.queryByRole('heading', { name: 'Choose your display name' })).not.toBeInTheDocument()
  expect(screen.getByText('Signed in as').closest('.signed-in-as')).toHaveTextContent(`Signed in as ${label}`)
  expect(screen.queryByRole('link', { name: /Signed in as/ })).not.toBeInTheDocument()
  expect(screen.queryByText(`${role}@example.com`)).not.toBeInTheDocument()
})

it('shows the name instead of the account ID in the mobile menu', async () => {
  setMobile(true)
  const user = userEvent.setup()
  renderApp(session('player', 'Iron Duke'))
  await user.click(screen.getByRole('button', { name: 'Menu' }))
  const navigation = screen.getByRole('navigation')
  expect(within(navigation).getByRole('link', { name: 'Signed in as Iron Duke' })).toHaveAttribute('href', '/profile')
  expect(within(navigation).queryByText(/issuer\|player/)).not.toBeInTheDocument()
  await user.click(within(navigation).getByRole('link', { name: 'Signed in as Iron Duke' }))
  expect(screen.getByRole('heading', { name: 'Display name' })).toBeInTheDocument()
  expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
})

it('changes the name later from the profile page', async () => {
  vi.mocked(fetch).mockResolvedValue(saved('Duke of Iron'))
  const user = userEvent.setup()
  renderApp(session('tester', 'Iron Duke'))
  await user.click(screen.getByRole('link', { name: 'Signed in as Iron Duke' }))

  const name = screen.getByLabelText('Display name')
  expect(name).toHaveValue('Iron Duke')
  await user.clear(name)
  await user.type(name, 'Duke of Iron')
  await user.click(screen.getByRole('button', { name: /SAVE NAME/ }))
  expect(await screen.findByText('Saved. You are signed in as Duke of Iron.')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Signed in as Duke of Iron' })).toBeInTheDocument()
})

it('offers the profile page only to Players and Testers', () => {
  renderApp(session('admin'), '/profile')
  expect(screen.getByRole('heading', { name: 'This route is outside the mapped frontier.' })).toBeInTheDocument()
})
