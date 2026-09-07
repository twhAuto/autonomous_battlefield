import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { BackgroundGenerator } from './BackgroundGenerator'
import { backgroundJob, generateBackground, generationOptions, GenerationError, type BackgroundJob, type GenerationOptions } from '../api/backgrounds'
vi.mock('../api/backgrounds', async original => ({ ...await original<typeof import('../api/backgrounds')>(), backgroundJob: vi.fn(), generateBackground: vi.fn(), generationOptions: vi.fn() }))
const input = { name: 'Northwind', doctrine: 'Defend', background: 'My unsaved history' }
const options: GenerationOptions = { available: true, modelLabel: 'Configured writer', remaining: 10, resetAt: new Date(Date.now() + 86400000).toISOString(), retryAt: new Date(Date.now() - 1000).toISOString() }
const job: BackgroundJob = { id: 'job', input, modelLabel: options.modelLabel, status: 'queued', background: '' }
const onUse = vi.fn()
function show(valid = true) { return render(<BackgroundGenerator input={input} valid={valid} onUse={onUse} />) }
beforeEach(() => { vi.clearAllMocks(); vi.mocked(generationOptions).mockResolvedValue(options); vi.mocked(generateBackground).mockResolvedValue(job); vi.mocked(backgroundJob).mockResolvedValue({ ...job, status: 'succeeded', background: 'Generated history' }); vi.spyOn(crypto, 'randomUUID').mockReturnValue('7f22e693-bacd-46bb-b942-64fa613cd388') })
afterEach(() => { vi.useRealTimers() })
it('previews editable output and applies only on Use', async () => {
 show(); await waitFor(() => expect(screen.getByRole('button', { name: 'Generate background' })).toBeEnabled())
 fireEvent.click(screen.getByRole('button', { name: 'Generate background' }))
 const preview = await screen.findByLabelText('Generated draft for Northwind')
 expect(generateBackground).toHaveBeenCalledWith(input, expect.any(String), expect.any(AbortSignal)); expect(onUse).not.toHaveBeenCalled()
 fireEvent.change(preview, { target: { value: ' Edited history ' } }); fireEvent.click(screen.getByRole('button', { name: 'Use background' }))
 expect(onUse).toHaveBeenCalledWith('Edited history'); expect(screen.queryByLabelText('Generated draft for Northwind')).not.toBeInTheDocument()
 expect(screen.getByText(/Save the faction to keep it/)).toBeInTheDocument()
})
it('discards and regenerates without changing the form', async () => {
 show(); await screen.findByText('Generation model: Configured writer'); fireEvent.click(screen.getByRole('button', { name: 'Generate background' }))
 await screen.findByLabelText('Generated draft for Northwind'); fireEvent.click(screen.getByRole('button', { name: 'Discard' }))
 expect(onUse).not.toHaveBeenCalled(); expect(screen.getByText('Draft discarded.')).toBeInTheDocument()
 await waitFor(() => expect(screen.getByRole('button', { name: 'Generate background' })).toBeEnabled()); fireEvent.click(screen.getByRole('button', { name: 'Generate background' })); await screen.findByLabelText('Generated draft for Northwind')
 expect(generateBackground).toHaveBeenCalledTimes(2)
 fireEvent.change(screen.getByLabelText('Generated draft for Northwind'), { target: { value: '😀'.repeat(1001) } }); expect(screen.getByRole('button', { name: 'Use background' })).toBeDisabled()
 fireEvent.change(screen.getByLabelText('Generated draft for Northwind'), { target: { value: ' ' } }); expect(screen.getByRole('button', { name: 'Use background' })).toBeDisabled()
})
it.each(['failed', 'expired'] as const)('handles %s jobs without applying anything', async status => {
 vi.mocked(backgroundJob).mockResolvedValue({ ...job, status, errorCategory: 'timeout' }); show(); await screen.findByText('Generation model: Configured writer'); fireEvent.click(screen.getByRole('button', { name: 'Generate background' }))
 await screen.findByText(status === 'expired' ? /reserved generation has been returned/ : /Generation failed/); expect(onUse).not.toHaveBeenCalled(); expect(screen.queryByRole('button', { name: 'Use background' })).not.toBeInTheDocument()
})
it('resumes an active job and polls queued/running states', async () => {
 vi.useFakeTimers(); vi.mocked(generationOptions).mockResolvedValueOnce({ ...options, activeJobId: job.id }); vi.mocked(backgroundJob).mockResolvedValueOnce(job).mockResolvedValueOnce({ ...job, status: 'running' }).mockResolvedValueOnce({ ...job, status: 'succeeded', background: 'History' })
 show(); await act(async () => {}); expect(screen.getByText('Waiting for an AI worker…')).toBeInTheDocument(); expect(screen.getByRole('button', { name: 'Generate background' })).toBeDisabled()
 await act(async () => { await vi.advanceTimersByTimeAsync(1500) }); expect(screen.getByText('Writing background…')).toBeInTheDocument()
 await act(async () => { await vi.advanceTimersByTimeAsync(1500) }); expect(screen.getByLabelText('Generated draft for Northwind')).toHaveValue('History'); expect(generateBackground).not.toHaveBeenCalled()
})
it('retries transient polling failures and does not replace edited previews on allowance refresh failure', async () => {
 vi.useFakeTimers(); vi.mocked(backgroundJob).mockRejectedValueOnce(new Error('Offline')).mockResolvedValueOnce({ ...job, status: 'succeeded', background: 'History' }); vi.mocked(generationOptions).mockResolvedValueOnce(options).mockRejectedValueOnce(new Error('Allowance offline'))
 show(); await act(async () => {}); fireEvent.click(screen.getByRole('button', { name: 'Generate background' })); await act(async () => {}); expect(screen.getByText('Offline')).toBeInTheDocument()
 await act(async () => { await vi.advanceTimersByTimeAsync(3000) }); fireEvent.change(screen.getByLabelText('Generated draft for Northwind'), { target: { value: 'My edit' } }); await act(async () => { await vi.advanceTimersByTimeAsync(4000) }); expect(screen.getByLabelText('Generated draft for Northwind')).toHaveValue('My edit')
})
it('honors disabled availability and validation while manual editing remains independent', async () => {
 vi.mocked(generationOptions).mockResolvedValue({ ...options, available: false, reason: 'Disabled by operator' }); show(false); await screen.findByText('Disabled by operator'); expect(screen.getByRole('button', { name: 'Generate background' })).toBeDisabled(); expect(onUse).not.toHaveBeenCalled()
})
it('keeps the idempotency key after uncertain network failures', async () => {
 vi.mocked(generateBackground).mockRejectedValueOnce(new GenerationError('Offline')); show(); await screen.findByText('Generation model: Configured writer'); fireEvent.click(screen.getByRole('button', { name: 'Generate background' })); await screen.findByText('Offline'); fireEvent.click(screen.getByRole('button', { name: 'Generate background' })); await screen.findByLabelText('Generated draft for Northwind')
 expect(vi.mocked(generateBackground).mock.calls[0][1]).toBe(vi.mocked(generateBackground).mock.calls[1][1]); expect(onUse).not.toHaveBeenCalled()
})
it('displays server quota limits and refreshes at UTC reset', async () => {
 vi.useFakeTimers(); const resetAt = new Date(Date.now() + 2000).toISOString(); vi.mocked(generateBackground).mockRejectedValueOnce(new GenerationError('Daily limit', 429, { ...options, remaining: 0, resetAt, retryAt: resetAt }))
 show(); await act(async () => {}); fireEvent.click(screen.getByRole('button', { name: 'Generate background' })); await act(async () => {}); expect(screen.getByText('Daily limit')).toBeInTheDocument(); expect(screen.getByRole('button', { name: 'Generate background' })).toBeDisabled()
 await act(async () => { await vi.advanceTimersByTimeAsync(2000) }); expect(screen.getByRole('button', { name: 'Generate background' })).toBeEnabled()
})
it('retries loading options and aborts polling on unmount', async () => {
 vi.mocked(generationOptions).mockRejectedValueOnce(new Error('Unavailable')); const view = show(); fireEvent.click(await screen.findByRole('button', { name: 'Retry generation availability' })); await screen.findByText('Generation model: Configured writer')
 vi.mocked(backgroundJob).mockImplementation(() => new Promise(() => {})); fireEvent.click(screen.getByRole('button', { name: 'Generate background' })); await waitFor(() => expect(backgroundJob).toHaveBeenCalled()); const signal = vi.mocked(backgroundJob).mock.calls[0][1]; view.unmount(); expect(signal?.aborted).toBe(true)
})
