import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { SessionContext } from '../../auth/SessionContext'
import { RolePreviewContext } from '../../auth/RolePreviewContext'
import { AdminSettingsPage } from './AdminSettingsPage'

const models = [
  { id: 'api', displayName: 'OpenAI API', modelLabel: 'Configured GPT', provider: 'openai', runtime: 'api', scope: 'cluster', available: true },
  { id: 'cli', displayName: 'Codex subscription', modelLabel: 'Configured Codex', provider: 'codex-cli', runtime: 'cli', scope: 'local', available: false, unavailableReason: 'Local runtime is offline.' },
]
function settings() { return { version: 0, models, policy: { providers: { openai: true, 'codex-cli': true }, profiles: { api: { enabled: true, allowedRoles: ['admin', 'developer'] }, cli: { enabled: true, allowedRoles: ['admin', 'developer'] } } } } }
function json(body: unknown, status = 200) { return new Response(JSON.stringify(body), { status }) }
function renderPage(setRole = vi.fn()) {
  return render(<MemoryRouter><SessionContext.Provider value={{ enabled: true, authenticated: true, actor: { id: 'actual-admin', role: 'admin' } }}><RolePreviewContext.Provider value={{ role: null, setRole }}><AdminSettingsPage /></RolePreviewContext.Provider></SessionContext.Provider></MemoryRouter>)
}
beforeEach(() => vi.stubGlobal('fetch', vi.fn()))

describe('AdminSettingsPage', () => {
  it('loads configured models, prevents player CLI access, and previews a real role', async () => {
    const user = userEvent.setup(), setRole = vi.fn()
    vi.mocked(fetch).mockResolvedValue(json(settings()))
    renderPage(setRole)
    expect(screen.getByRole('status')).toHaveTextContent('Loading model access settings')
    expect(await screen.findByText('Configured GPT · API')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith(expect.stringContaining('/v1/admin/model-access'), expect.objectContaining({ signal: expect.any(AbortSignal) }))
    const cli = within(screen.getByText('Codex subscription').closest('article')!)
    expect(cli.getByText('Local runtime is offline.')).toBeInTheDocument()
    expect(cli.getByRole('checkbox', { name: 'Player' })).toBeDisabled()
    expect(screen.getByText('actual-admin · Administrator')).toBeInTheDocument()
    expect(screen.queryByText(/invite member|audit log|monthly budget/i)).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'View as Player' }))
    expect(setRole).toHaveBeenCalledWith('player')
  })

  it('saves provider/profile access and roles to the server and reloads saved values', async () => {
    const user = userEvent.setup()
    let persisted = settings()
    vi.mocked(fetch).mockImplementation(async (_url, init) => {
      if (init?.method === 'PUT') {
        persisted = { ...persisted, ...JSON.parse(String(init.body)), version: 1 }
        return json({ version: persisted.version, policy: persisted.policy })
      }
      return json(persisted)
    })
    const { unmount } = renderPage()
    await screen.findByText('Configured GPT · API')
    const api = within(screen.getByText('OpenAI API').closest('article')!)
    await user.click(api.getByRole('checkbox', { name: 'Player' }))
    await user.click(screen.getByRole('switch', { name: 'Codex subscription model access' }))
    await user.click(screen.getByRole('switch', { name: 'openai provider access' }))
    expect(api.getByRole('switch')).toBeDisabled()
    expect(screen.getByRole('button', { name: 'View as Player' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    expect(await screen.findByText('Changes saved')).toBeInTheDocument()
    expect(persisted.policy.providers.openai).toBe(false)
    expect(persisted.policy.profiles.api.allowedRoles).toContain('player')
    expect(persisted.policy.profiles.cli.enabled).toBe(false)
    unmount(); renderPage()
    expect(await screen.findByRole('switch', { name: 'openai provider access' })).not.toBeChecked()
    expect(screen.getByRole('switch', { name: 'Codex subscription model access' })).not.toBeChecked()
  })

  it('preserves edits on save failure and handles conflicts with discard and refresh', async () => {
    const user = userEvent.setup()
    vi.mocked(fetch).mockResolvedValueOnce(json(settings())).mockResolvedValueOnce(json({}, 503)).mockResolvedValueOnce(json({}, 409)).mockResolvedValueOnce(json(settings()))
    renderPage()
    await screen.findByText('Configured GPT · API')
    await user.click(screen.getByRole('switch', { name: 'openai provider access' }))
    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Changes could not be saved')
    expect(screen.getByRole('switch', { name: 'openai provider access' })).not.toBeChecked()
    expect(screen.queryByText('Changes saved')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Another administrator changed')
    await user.click(screen.getByRole('button', { name: 'Discard changes' }))
    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByRole('switch', { name: 'openai provider access' })).toBeChecked()
  })

  it('can remove all roles without restoring default access', async () => {
    const user = userEvent.setup()
    vi.mocked(fetch).mockResolvedValue(json(settings()))
    renderPage(); await screen.findByText('Configured GPT · API')
    const api = within(screen.getByText('OpenAI API').closest('article')!)
    await user.click(api.getByRole('checkbox', { name: 'Administrator' }))
    await user.click(api.getByRole('checkbox', { name: 'Developer' }))
    expect(api.getByText('No roles allowed')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Discard changes' }))
    expect(api.getByRole('checkbox', { name: 'Administrator' })).toBeChecked()
  })

  it('retries loading failures and reports an empty catalog', async () => {
    const user = userEvent.setup()
    vi.mocked(fetch).mockResolvedValueOnce(json({}, 503)).mockResolvedValueOnce(json({ version: 0, models: [], policy: { providers: {}, profiles: {} } }))
    renderPage()
    expect(await screen.findByRole('alert')).toHaveTextContent('Model access settings could not be loaded')
    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByText('No AI model profiles are configured.')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('warns before leaving only while edits are unsaved', async () => {
    const user = userEvent.setup()
    vi.mocked(fetch).mockResolvedValue(json(settings()))
    const { unmount } = renderPage()
    await screen.findByText('Configured GPT · API')
    const leave = () => {
      const event = new Event('beforeunload', { cancelable: true })
      window.dispatchEvent(event)
      return event.defaultPrevented
    }
    expect(leave()).toBe(false)
    await user.click(screen.getByRole('switch', { name: 'openai provider access' }))
    expect(leave()).toBe(true)
    await user.click(screen.getByRole('button', { name: 'Discard changes' }))
    expect(leave()).toBe(false)
    await user.click(screen.getByRole('switch', { name: 'openai provider access' }))
    unmount()
    expect(leave()).toBe(false)
  })

  it('aborts pending reads on unmount', async () => {
    let finish!: (value: Response) => void
    vi.mocked(fetch).mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const { unmount } = renderPage()
    const signal = vi.mocked(fetch).mock.calls[0][1]?.signal
    unmount(); expect(signal?.aborted).toBe(true)
    await act(async () => { finish(json(settings())) })
  })
})
