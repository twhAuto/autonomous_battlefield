import type { ReactNode } from 'react'

// Roving focus with automatic activation follows the ARIA tabs keyboard pattern.
export function InspectorTabs({ id, label, tabs, selected, onSelect }: Readonly<{
  id: string; label: string; tabs: { id: string; label: ReactNode }[]; selected: string; onSelect: (id: string) => void
}>) {
  return <div className="inspector-tabs" role="tablist" aria-label={label}>
    {tabs.map((tab, index) => <button key={tab.id} id={`${id}-tab-${tab.id}`} type="button" role="tab" aria-selected={selected === tab.id} aria-controls={`${id}-panel-${tab.id}`} tabIndex={selected === tab.id ? 0 : -1} onClick={() => onSelect(tab.id)} onKeyDown={(event) => {
      let next: number
      if (event.key === 'ArrowRight') next = (index + 1) % tabs.length
      else if (event.key === 'ArrowLeft') next = (index + tabs.length - 1) % tabs.length
      else if (event.key === 'Home') next = 0
      else if (event.key === 'End') next = tabs.length - 1
      else return
      event.preventDefault()
      onSelect(tabs[next].id)
      document.getElementById(`${id}-tab-${tabs[next].id}`)?.focus()
    }}>{tab.label}</button>)}
  </div>
}
