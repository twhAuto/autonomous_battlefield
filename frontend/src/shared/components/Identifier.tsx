import { useState } from 'react'

export function Identifier({ value, label = 'Identifier' }: Readonly<{ value: string; label?: string }>) {
  const [message, setMessage] = useState('')
  const [open, setOpen] = useState(false)
  async function copy() {
    try { await navigator.clipboard.writeText(value); setMessage(`${label} copied.`) }
    catch { setOpen(true); setMessage('Copy unavailable. Select and copy the full identifier below.') }
  }
  return <div className="identifier">
    <div className="identifier__short"><code>{value.slice(0, 8)}</code><button type="button" onClick={() => void copy()} aria-label={`Copy ${label.toLowerCase()}`}>Copy</button></div>
    <details open={open} onToggle={event => setOpen(event.currentTarget.open)}><summary>{label} details</summary><code className="identifier__full">{value}</code></details>
    <span role="status">{message}</span>
  </div>
}
