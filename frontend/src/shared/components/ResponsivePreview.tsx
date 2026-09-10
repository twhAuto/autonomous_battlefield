import { type ReactNode, useState } from 'react'
import { useMobile } from '../hooks/useMobile'

export function ResponsivePreview({ label, children }: Readonly<{ label: string; children: ReactNode }>) {
  const mobile = useMobile()
  const [open, setOpen] = useState(false)
  return <details className="responsive-preview" open={!mobile || open} onToggle={event => { if (mobile) setOpen(event.currentTarget.open) }}>
    <summary>{label}</summary>
    {children}
  </details>
}
