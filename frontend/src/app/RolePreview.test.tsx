import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { expect, it, vi } from 'vitest'
import App from './App'
import { apiFetch, createApiClient, setRequestPreviewRole } from '../shared/api/request'

it('previews across navigation, sends the effective role, and restores admin access', async () => {
  const user = userEvent.setup()
  const fetcher = vi.fn(async (url: string, _init?: RequestInit) => {
    const body = url.includes('/admin/model-access') ? { version: 0, models: [], policy: { providers: {}, profiles: {} } }
      : url.includes('/model-profiles') ? { profiles: [] }
      : url.includes('/scenarios') ? { scenarios: [] } : { factions: [] }
    return new Response(JSON.stringify(body))
  })
  vi.stubGlobal('fetch', fetcher)
  render(<MemoryRouter initialEntries={['/admin']}><App initialSession={{ authenticated: true, enabled: true, actor: { id: 'owner', role: 'admin' } }} /></MemoryRouter>)
  await screen.findByText('No AI model profiles are configured.')
  await user.click(screen.getByRole('button', { name: 'View as Player' }))
  expect(screen.getByRole('status')).toHaveTextContent('Viewing as Player')
  expect(screen.queryByRole('link', { name: 'Admin' })).not.toBeInTheDocument()
  await waitFor(() => expect(fetcher).toHaveBeenCalledWith(expect.stringContaining('/v1/model-profiles'), expect.objectContaining({ headers: expect.objectContaining({ 'X-Battlefield-Preview-Role': 'player' }) })))
  await user.click(screen.getByRole('link', { name: 'Factions' }))
  expect(screen.getByRole('status')).toHaveTextContent('Viewing as Player')
  await user.click(screen.getByRole('button', { name: 'Exit preview' }))
  expect(screen.queryByText(/Viewing as Player/)).not.toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Admin' })).toBeInTheDocument()
  await user.click(screen.getByRole('link', { name: 'Admin' }))
  await screen.findByText('No AI model profiles are configured.')
  await user.click(screen.getByRole('button', { name: 'View as Tester' }))
  expect(screen.getByRole('status')).toHaveTextContent('Viewing as Tester')
})

it('blocks mutations in preview before sending them and preserves a clear error', async () => {
  const fetcher = vi.fn().mockResolvedValue(new Response('{}'))
  vi.stubGlobal('fetch', fetcher)
  setRequestPreviewRole('player')
  try {
    await expect(createApiClient(Error)('/v1/battles', { method: 'POST' })).rejects.toThrow('Role preview is read-only')
    expect(fetcher).not.toHaveBeenCalled()
    await apiFetch('/v1/model-profiles')
    expect(fetcher).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ headers: expect.objectContaining({ 'X-Battlefield-Preview-Role': 'player' }) }))
  } finally { setRequestPreviewRole(null) }
  await apiFetch('/v1/factions')
  expect(fetcher.mock.calls.at(-1)?.[1].headers).not.toHaveProperty('X-Battlefield-Preview-Role')
})
