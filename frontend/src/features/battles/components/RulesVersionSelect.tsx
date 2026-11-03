// Chooses a rules version to filter by: every version, or one that recorded battles ran
// on, with the current version marked. Battle history, the league list and Statistics
// share it so all offer the same versions.
import { useEffect, useState } from 'react'
import { listRulesVersions, type RulesVersions } from '../api/battles'

/** Names rules versions for display, newest first as given: "v13" or "v13, v12". */
export function rulesVersionsLabel(versions: readonly string[]) {
  return versions.map(version => `v${version}`).join(', ')
}

/**
 * An empty value selects every version. With currentByDefault, an empty value selects
 * the current version and 'all' every version, as GET /v1/analytics reads them.
 */
export function RulesVersionSelect({ value, onChange, currentByDefault = false }: Readonly<{
  value: string; onChange: (version: string) => void; currentByDefault?: boolean
}>) {
  const [available, setAvailable] = useState<RulesVersions | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    // Without the list, the filter still offers its default, every version and the selected one.
    listRulesVersions(controller.signal).then(setAvailable).catch(() => undefined)
    return () => controller.abort()
  }, [])

  const versions = available?.versions ?? []
  const current = available?.current ?? ''
  const every = currentByDefault ? 'all' : ''
  // The current version is the empty value when it is the default, however the URL names it.
  const optionValue = (version: string) => currentByDefault && version === current ? '' : version
  const selected = optionValue(value)
  return (
    <label>Rules <select value={selected} onChange={event => onChange(event.target.value)}>
      <option value={every}>All versions</option>
      {currentByDefault && !current && <option value="">Current version</option>}
      {selected && selected !== every && !versions.includes(selected) && <option value={selected}>v{selected}</option>}
      {versions.map(version => <option key={version} value={optionValue(version)}>v{version}{version === current ? ' (current)' : ''}</option>)}
    </select></label>
  )
}
