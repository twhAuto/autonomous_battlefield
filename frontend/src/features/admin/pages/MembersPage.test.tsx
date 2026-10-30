import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import App from '../../../app/App'
import { MembersPage } from './MembersPage'
import type { Member } from '../api/members'

const seed: Member = { id: 'one', email: 'friend@example.com', desiredAccess: true, version: 1, syncedVersion: 0, updatedAt: '2026-09-19T00:00:00Z' }
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status })
function renderPage() { return render(<MemoryRouter><MembersPage /></MemoryRouter>) }
beforeEach(() => vi.stubGlobal('fetch', vi.fn()))
afterEach(() => vi.useRealTimers())

it('grants, removes and reinvites a Player and copies the game URL', async () => {
  const user = userEvent.setup()
  let members: Member[] = []
  vi.mocked(fetch).mockImplementation(async (_url, init) => {
    if (init?.method === 'POST') {
      const body = JSON.parse(init.body as string) as { email: string }
      members = [{ ...seed, email: body.email }]
      return new Response(null, { status: 202 })
    }
    if (init?.method === 'DELETE') { members = [{ ...seed, desiredAccess: false, version: 2 }]; return new Response(null, { status: 202 }) }
    return json({ members, gameUrl: 'https://game.example', syncError: '' })
  })
  renderPage()
  expect(screen.getByText('Loading members…')).toBeInTheDocument()
  await screen.findByText('No Players have been invited yet.')
  await user.type(screen.getByLabelText('Email address'), seed.email)
  await user.click(screen.getByRole('button', { name: 'Grant access' }))
  await screen.findByText(/Waiting for access/)
  expect(screen.getByRole('status')).toHaveTextContent('Invitation saved')
  expect(screen.getByLabelText('Email address')).toHaveValue('')
  await user.click(screen.getByRole('button', { name: `Remove access for ${seed.email}` }))
  await screen.findByText(/Removed · Cloudflare update pending/)
  expect(screen.getByRole('status')).toHaveTextContent('Access removed from the game')
  await user.click(screen.getByRole('button', { name: `Grant access to ${seed.email}` }))
  await screen.findByText(/Waiting for access/)
  members = [{ ...seed, syncedVersion: 1 }]
  await user.click(screen.getByRole('button', { name: 'Refresh' }))
  await screen.findByText('Ready to play · Cloudflare synchronized')
  await user.click(screen.getByRole('button', { name: 'Copy game link' }))
  expect(await navigator.clipboard.readText()).toBe('https://game.example')
  expect(screen.getByRole('status')).toHaveTextContent('Game link copied')
})

it('shows Cloudflare errors and requests synchronization retry', async () => {
  const user = userEvent.setup()
  vi.mocked(fetch).mockImplementation(async (_url, init) => init?.method === 'POST' ? new Response(null, { status: 202 }) : json({ members: [seed], gameUrl: 'https://game.example', syncError: 'Cloudflare returned HTTP 429' }))
  renderPage()
  expect(await screen.findByRole('alert')).toHaveTextContent('HTTP 429')
  await user.click(screen.getByRole('button', { name: 'Retry synchronization' }))
  expect(fetch).toHaveBeenCalledWith(expect.stringContaining('/v1/admin/members/retry'), expect.objectContaining({ method: 'POST' }))
  expect(screen.getByRole('status')).toHaveTextContent('retry requested')
})

it('keeps failed invitations editable and reports clipboard failure', async () => {
  const user = userEvent.setup()
  vi.mocked(fetch).mockImplementation(async (_url, init) => init?.method === 'POST' ? json({}, 409) : json({ members: [], gameUrl: 'https://game.example', syncError: '' }))
  renderPage(); await screen.findByText('No Players have been invited yet.')
  await user.type(screen.getByLabelText('Email address'), 'admin@example.com')
  await user.click(screen.getByRole('button', { name: 'Grant access' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Administrator and tester')
  expect(screen.getByLabelText('Email address')).toHaveValue('admin@example.com')
  vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(new Error('blocked'))
  await user.click(screen.getByRole('button', { name: 'Copy game link' }))
  expect(screen.getByRole('alert')).toHaveTextContent('Select and copy')
})

it('reports load failure and recovers on refresh', async () => {
  const user = userEvent.setup()
  vi.mocked(fetch).mockResolvedValue(json({}, 503))
  renderPage()
  expect(await screen.findByRole('alert')).toHaveTextContent('member management is configured')
  expect(screen.getByRole('button', { name: 'Grant access' })).toBeDisabled()
  vi.mocked(fetch).mockResolvedValue(json({ members: [], gameUrl: 'https://game.example', syncError: '' }))
  await user.click(screen.getByRole('button', { name: 'Refresh' }))
  await screen.findByText('No Players have been invited yet.')
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})

it('polls synchronization without overlapping requests and stops after unmount', async () => {
  vi.useFakeTimers()
  vi.mocked(fetch).mockResolvedValue(json({ members: [seed], gameUrl: 'https://game.example', syncError: '' }))
  const view = renderPage()
  await act(async () => { await vi.advanceTimersByTimeAsync(1) })
  vi.mocked(fetch).mockResolvedValue(json({ members: [{ ...seed, syncedVersion: 1 }], gameUrl: 'https://game.example', syncError: '' }))
  await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
  expect(screen.getByText('Ready to play · Cloudflare synchronized')).toBeInTheDocument()
  view.unmount(); const calls = vi.mocked(fetch).mock.calls.length
  await act(async () => { await vi.advanceTimersByTimeAsync(10000) })
  expect(fetch).toHaveBeenCalledTimes(calls)
})

it('exposes the members route to Admins and never loads it for Players or Testers', async () => {
  vi.mocked(fetch).mockResolvedValue(json({ members: [], gameUrl: 'https://game.example', syncError: '' }))
  const view = render(<MemoryRouter initialEntries={['/admin/members']}><App initialSession={{ authenticated: true, enabled: true, mode: 'cloudflare', actor: { id: 'owner', role: 'admin' }, capabilities: { inspect: true, admin: true, members: true } }} /></MemoryRouter>)
  await screen.findByRole('heading', { name: 'Members' }); view.unmount()
  for (const role of ['player', 'tester', 'admin'] as const) {
    vi.mocked(fetch).mockClear()
    const other = render(<MemoryRouter initialEntries={['/admin/members']}><App initialSession={{ authenticated: true, enabled: true, mode: 'cloudflare', actor: { id: 'someone', role } }} /></MemoryRouter>)
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Members' })).not.toBeInTheDocument())
    expect(fetch).not.toHaveBeenCalledWith(expect.stringContaining('/v1/admin/members'), expect.anything())
    other.unmount()
  }
})
