// Chooses a rules version to filter a list by: every version, or one that recorded
// battles ran on, with the current version marked. Battle history and the league list
// share it so both offer the same versions.
import { useEffect, useState } from 'react'
import { listRulesVersions, type RulesVersions } from '../api/battles'

/** Names rules versions for display, newest first as given: "v13" or "v13, v12". */
export function rulesVersionsLabel(versions: readonly string[]) {
  return versions.map(version => `v${version}`).join(', ')
}

export function RulesVersionSelect({ value, onChange }: Readonly<{ value: string; onChange: (version: string) => void }>) {
  const [available, setAvailable] = useState<RulesVersions | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    // Without the list, the filter still offers every version and the selected one.
    listRulesVersions(controller.signal).then(setAvailable).catch(() => undefined)
    return () => controller.abort()
  }, [])

  const versions = available?.versions ?? []
  return (
    <label>Rules <select value={value} onChange={event => onChange(event.target.value)}>
      <option value="">All versions</option>
      {value && !versions.includes(value) && <option value={value}>v{value}</option>}
      {versions.map(version => <option key={version} value={version}>v{version}{version === available?.current ? ' (current)' : ''}</option>)}
    </select></label>
  )
}
