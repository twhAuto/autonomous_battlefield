import { maxBackgroundLength } from '../limits'
import { useEffect, useRef, useState } from 'react'
import { backgroundJob, generateBackground, generationOptions, GenerationError, type BackgroundInput, type GenerationOptions } from '../api/backgrounds'

export function BackgroundGenerator({ input, valid, onUse }: Readonly<{ input: BackgroundInput; valid: boolean; onUse: (value: string) => void }>) {
 const [options, setOptions] = useState<GenerationOptions>()
 const [activeId, setActiveId] = useState('')
 const [phase, setPhase] = useState('')
 const [busy, setBusy] = useState(false)
 const [message, setMessage] = useState('')
 const [preview, setPreview] = useState<string | null>(null)
 const [draftName, setDraftName] = useState('')
 const [now, setNow] = useState(Date.now)
 const [revision, setRevision] = useState(0)
 const pending = useRef<{ input: string; key: string } | null>(null)
 const controller = useRef<AbortController | null>(null)
 useEffect(() => {
  const abort = new AbortController()
  generationOptions(abort.signal).then(value => {
   if (abort.signal.aborted) return
   setOptions(value)
   if (value.activeJobId) { setActiveId(value.activeJobId); setPhase('queued') }
  }).catch(error => { if (!abort.signal.aborted) setMessage(error instanceof Error ? error.message : 'Generation is unavailable.') })
  return () => abort.abort()
 }, [revision])
 useEffect(() => {
  const timer = window.setInterval(() => setNow(Date.now()), 1000)
  return () => { window.clearInterval(timer); controller.current?.abort() }
 }, [])
 useEffect(() => {
  if (!activeId) return
  const abort = new AbortController()
  let timer: ReturnType<typeof setTimeout>
  async function poll() {
   try {
    const job = await backgroundJob(activeId, abort.signal)
    if (abort.signal.aborted) return
    setPhase(job.status)
    if (job.status === 'queued' || job.status === 'running') { timer = setTimeout(poll, 1500); return }
    if (job.status === 'succeeded') { setPreview(job.background); setDraftName(job.input.name); setMessage('Review this draft before using it.') }
    else setMessage(job.status === 'expired' ? 'No worker started in time. Your reserved generation has been returned.' : `Generation failed (${job.errorCategory || 'model error'}). Your current background is unchanged.`)
    setActiveId('')
    setOptions(undefined)
    setRevision(value => value + 1)
   } catch (error) {
    if (!abort.signal.aborted) { setMessage(error instanceof Error ? error.message : 'Could not check generation.'); timer = setTimeout(poll, 3000) }
   }
  }
  void poll()
  return () => { abort.abort(); clearTimeout(timer) }
 }, [activeId])
 const seconds = options ? Math.max(0, Math.ceil((Date.parse(options.retryAt) - now) / 1000)) : 0
 useEffect(() => {
  if (options && Date.parse(options.resetAt) <= now) { setOptions(undefined); setRevision(value => value + 1) }
 }, [now, options])
 const blocked = !valid || !options?.available || options.remaining <= 0 || seconds > 0 || busy || Boolean(activeId)
 async function generate() {
  if (blocked) return
  const serialized = JSON.stringify(input)
  if (!pending.current || pending.current.input !== serialized) pending.current = { input: serialized, key: crypto.randomUUID() }
  setBusy(true); setMessage('')
  controller.current = new AbortController()
  try {
   const job = await generateBackground(input, pending.current.key, controller.current.signal)
   pending.current = null
   setActiveId(job.id); setPhase(job.status)
  } catch (error) {
   if (controller.current.signal.aborted) return
   setMessage(error instanceof Error ? error.message : 'Could not generate background.')
   if (error instanceof GenerationError) {
    if (error.status) pending.current = null
    if (error.options) { setOptions(error.options); if (error.options.activeJobId) setActiveId(error.options.activeJobId) }
   }
  } finally { if (!controller.current.signal.aborted) setBusy(false) }
 }
 const previewCount = Array.from(preview?.trim() ?? '').length
 return <section className="background-generator" aria-label="Background generation">
  <p>{options?.modelLabel ? `Generation model: ${options.modelLabel}` : 'AI background writer'}</p>
  {options && <p>{options.remaining} generations left today · resets {new Date(options.resetAt).toLocaleString()} (00:00 UTC)</p>}
  {seconds > 0 && <p>Available again in {seconds} seconds.</p>}
  {!options?.available && options?.reason && <p>{options.reason}</p>}
  <button type="button" disabled={blocked} onClick={generate}>{busy ? 'Queuing…' : 'Generate background'}</button>
  {!options && message && <button type="button" onClick={() => setRevision(value => value + 1)}>Retry generation availability</button>}
  <div aria-live="polite">{activeId && <p>{phase === 'running' ? 'Writing background…' : 'Waiting for an AI worker…'}</p>}{message && <p>{message}</p>}</div>
  {preview !== null && <div className="background-draft">
   <label htmlFor="background-draft">Generated draft for {draftName}</label>
   <textarea id="background-draft" rows={6} value={preview} onChange={event => setPreview(event.target.value)} />
   <p>{previewCount} / {maxBackgroundLength.toLocaleString('en-US')} characters. Using this draft updates the form; save the faction to keep it.</p>
   <button type="button" disabled={previewCount === 0 || previewCount > maxBackgroundLength} onClick={() => { onUse(preview.trim()); setPreview(null); setMessage('Background added to the form. Save the faction to keep it.') }}>Use background</button>
   <button type="button" onClick={() => { setPreview(null); setMessage('Draft discarded.') }}>Discard</button>
  </div>}
 </section>
}
