// Checks battle-history filters, cursor navigation, browser history, stale-request
// handling, and return links from battle details.

import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import { BattleHistoryPage } from './BattleHistoryPage'
import { listBattles, type BattleSummary } from '../api/battles'
import { BattleHeader } from '../components/BattlePresentation'
import type { BattleRun } from '../api/battles'

vi.mock('../api/battles', async (original) => ({ ...await original<typeof import('../api/battles')>(), listBattles: vi.fn() }))
const summary: BattleSummary = {
  id: '00000000-0000-0000-0000-000000000001', scenarioId: 'siege',
  participants: [{ id: 'north', name: 'North' }, { id: 'south', name: 'South' }],
  status: 'completed', paused: false, requestedTicks: 25, currentTick: 20,
  createdAt: '2026-09-07T00:00:00Z', startedAt: null, completedAt: null,
  outcome: { winnerFactionId: 'north', reason: 'castle_destroyed' },
}
function LocationProbe() {
  const location = useLocation()
  const navigate = useNavigate()
  return <><output data-testid="location">{location.pathname + location.search}</output><button onClick={() => navigate(-1)}>Browser back</button></>
}
function renderHistory(path = '/battles') {
  return render(<MemoryRouter initialEntries={[path]}>
    <LocationProbe />
    <Routes>
      <Route path="/battles" element={<BattleHistoryPage />} />
      <Route path="/battles/:id" element={<BattleHeader battle={{ ...summary, attempt: 1 } as unknown as BattleRun} battleTitle="North vs South" connection="settled" connectionMessage="" retryConnection={() => {}} />} />
    </Routes>
  </MemoryRouter>)
}
beforeEach(() => { vi.mocked(listBattles).mockReset() })

it('renders summaries and distinguishes a draw from absent outcomes', async () => {
  vi.mocked(listBattles).mockResolvedValue({ battles: [
    summary,
    { ...summary, id: 'draw', outcome: { winnerFactionId: null, reason: 'tick_limit' } },
    { ...summary, id: 'unknown-winner', outcome: { winnerFactionId: 'old-id', reason: 'castle_destroyed' } },
    { ...summary, id: 'missing', outcome: null },
    { ...summary, id: 'failed', status: 'failed', outcome: null },
    { ...summary, id: 'stopped', status: 'stopped', outcome: null },
    { ...summary, id: 'paused', status: 'running', paused: true, outcome: null },
  ], nextCursor: null })
  renderHistory()
  expect(await screen.findByText('North wins')).toBeInTheDocument()
  expect(screen.getAllByText('Draw')).toHaveLength(1)
  expect(screen.getByText('old-id wins')).toBeInTheDocument()
  expect(screen.getAllByText('—')).toHaveLength(4)
  expect(screen.getByText(summary.id)).toBeInTheDocument()
  expect(screen.getAllByText('20 / 25 ticks')).toHaveLength(7)
  expect(screen.getByRole('button', { name: /previous/i })).toBeDisabled()
  expect(screen.getByRole('button', { name: /next/i })).toBeDisabled()
})

it('paginates and restores earlier cursors', async () => {
  const user = userEvent.setup()
  vi.mocked(listBattles).mockImplementation(async ({ cursor }) => ({ battles: [{ ...summary, id: cursor || 'first' }], nextCursor: cursor === 'third' ? null : cursor === 'second' ? 'third' : 'second' }))
  renderHistory('/battles?status=completed')
  await screen.findByText('first', { selector: '.identifier__short code' })
  await user.click(screen.getByRole('button', { name: /next/i }))
  await screen.findByText('second', { selector: '.identifier__short code' })
  expect(screen.getByTestId('location')).toHaveTextContent('status=completed&previous=&cursor=second')
  await user.click(screen.getByRole('button', { name: /next/i }))
  await screen.findByText('third', { selector: '.identifier__short code' })
  await user.click(screen.getByRole('button', { name: /previous/i }))
  await screen.findByText('second', { selector: '.identifier__short code' })
  await user.click(screen.getByRole('button', { name: /previous/i }))
  await screen.findByText('first', { selector: '.identifier__short code' })
  expect(screen.getByTestId('location').textContent).toBe('/battles?status=completed')
})

it('restores the previous page through browser back', async () => {
  const user = userEvent.setup()
  vi.mocked(listBattles).mockImplementation(async ({ cursor }) => ({ battles: [{ ...summary, id: cursor || 'first' }], nextCursor: cursor ? null : 'second' }))
  renderHistory('/battles?status=completed')
  await screen.findByText('first', { selector: '.identifier__short code' })
  await user.click(screen.getByRole('button', { name: /next/i }))
  await screen.findByText('second', { selector: '.identifier__short code' })
  await user.click(screen.getByRole('button', { name: 'Browser back' }))
  await screen.findByText('first', { selector: '.identifier__short code' })
  expect(screen.getByTestId('location').textContent).toBe('/battles?status=completed')
})

it('refreshes an older page back to the newest results', async () => {
  const user = userEvent.setup()
  vi.mocked(listBattles).mockImplementation(async ({ cursor }) => ({ battles: [{ ...summary, id: cursor || 'first' }], nextCursor: cursor ? null : 'second' }))
  renderHistory('/battles?status=completed')
  await screen.findByText('first', { selector: '.identifier__short code' })
  await user.click(screen.getByRole('button', { name: /next/i }))
  await screen.findByText('second', { selector: '.identifier__short code' })
  await user.click(screen.getByRole('button', { name: /refresh/i }))
  await screen.findByText('first', { selector: '.identifier__short code' })
  expect(screen.getByTestId('location').textContent).toBe('/battles?status=completed')
})

it('resets pagination when filtering and distinguishes filtered empty results', async () => {
  const user = userEvent.setup()
  vi.mocked(listBattles).mockResolvedValue({ battles: [], nextCursor: null })
  renderHistory('/battles?cursor=old')
  await screen.findByText('No matching battles')
  await user.selectOptions(screen.getByRole('combobox', { name: 'Status' }), 'paused')
  await waitFor(() => expect(listBattles).toHaveBeenLastCalledWith({ status: 'paused', cursor: '' }, expect.any(AbortSignal)))
  expect(screen.getByTestId('location').textContent).toBe('/battles?status=paused')
  await user.selectOptions(screen.getByRole('combobox', { name: 'Status' }), '')
  await screen.findByText('No battles yet')
  expect(screen.getByTestId('location').textContent).toBe('/battles')
})

it('supports loading, errors, retry, and refresh on the first page', async () => {
  const user = userEvent.setup()
  vi.mocked(listBattles).mockRejectedValueOnce(new Error('Unavailable')).mockResolvedValue({ battles: [], nextCursor: null })
  renderHistory()
  expect(await screen.findByRole('alert')).toHaveTextContent('Unavailable')
  await user.click(screen.getByRole('button', { name: 'RETRY' }))
  await screen.findByText('No battles yet')
  expect(screen.getByRole('link', { name: 'STAGE BATTLE →' })).toHaveAttribute('href', '/battles/new')
  await user.click(screen.getByRole('button', { name: 'REFRESH' }))
  await waitFor(() => expect(listBattles).toHaveBeenCalledTimes(3))
})

it('does not display stale successful or failed requests after changing filters', async () => {
  const user = userEvent.setup()
  let resolveFirst!: (value: Awaited<ReturnType<typeof listBattles>>) => void
  let rejectSecond!: (error: unknown) => void
  vi.mocked(listBattles)
    .mockImplementationOnce(() => new Promise((resolve) => { resolveFirst = resolve }))
    .mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectSecond = reject }))
    .mockResolvedValue({ battles: [], nextCursor: null })
  renderHistory()
  expect(screen.getByText('Loading battle history…')).toHaveRole('status')
  const firstSignal = vi.mocked(listBattles).mock.calls[0][1]
  await user.selectOptions(screen.getByRole('combobox'), 'running')
  expect(firstSignal?.aborted).toBe(true)
  await user.selectOptions(screen.getByRole('combobox'), 'failed')
  await screen.findByText('No matching battles')
  await act(async () => {
    resolveFirst({ battles: [summary], nextCursor: null })
    rejectSecond(new Error('Obsolete'))
  })
  expect(screen.queryByText('North wins')).not.toBeInTheDocument()
  expect(screen.queryByText('Obsolete')).not.toBeInTheDocument()
})

it('cancels on unmount, handles non-Error failures, and allows correcting unknown URL filters', async () => {
  const user = userEvent.setup()
  vi.mocked(listBattles).mockRejectedValueOnce('failure').mockResolvedValue({ battles: [], nextCursor: null })
  const view = renderHistory('/battles?status=unknown')
  expect(await screen.findByRole('alert')).toHaveTextContent('Battle history could not be loaded.')
  expect(screen.getByRole('option', { name: 'Unknown status' })).toBeInTheDocument()
  await user.selectOptions(screen.getByRole('combobox'), '')
  await screen.findByText('No battles yet')
  const signal = vi.mocked(listBattles).mock.calls.at(-1)?.[1]
  view.unmount()
  expect(signal?.aborted).toBe(true)
})

it('opens details and returns to the originating history URL', async () => {
  const user = userEvent.setup()
  vi.mocked(listBattles).mockResolvedValue({ battles: [summary], nextCursor: null })
  renderHistory('/battles?status=completed&cursor=page2&previous=')
  await user.click(await screen.findByRole('link', { name: 'Open battle →' }))
  expect(screen.getByRole('link', { name: '← BATTLE HISTORY' })).toHaveAttribute('href', '/battles?status=completed&cursor=page2&previous=')
  await user.click(screen.getByRole('link', { name: '← BATTLE HISTORY' }))
  await screen.findByRole('table')
  expect(listBattles).toHaveBeenLastCalledWith({ status: 'completed', cursor: 'page2' }, expect.any(AbortSignal))
})

it('links direct detail visits back to history', () => {
  renderHistory('/battles/' + summary.id)
  expect(screen.getByRole('link', { name: '← BATTLE HISTORY' })).toHaveAttribute('href', '/battles')
})

it('restores the first page from a directly loaded cursor without prior cursors', async () => {
  const user = userEvent.setup()
  vi.mocked(listBattles).mockResolvedValue({ battles: [], nextCursor: null })
  renderHistory('/battles?cursor=older')
  await screen.findByText('No matching battles')
  await user.click(screen.getByRole('button', { name: /previous/i }))
  expect(await screen.findByText('No battles yet')).toBeInTheDocument()
})
