// Chooses a battle controller: scripted, or a model profile with its model and
// reasoning effort. Battle setup and league setup share it so both submit the same
// selections.
import { usePermissions } from '../../auth/SessionContext'
import type { ModelProfile } from '../../../shared/domain/model-profile'

const defaultReasoningEffort = 'low'

export type ModelSelection = { modelIdentifier?: string; reasoningEffort?: string }
function supportedEfforts(profile: ModelProfile | undefined, model: string) {
  return profile?.modelOptions?.find(option => option.id === model)?.reasoningEfforts ?? []
}

// Prefer an explicit Low effort over the provider's unstated default; models without
// configurable efforts keep sending none.
export function resolveSelection(profile: ModelProfile | undefined, selection: ModelSelection): ModelSelection {
  if (!profile || selection.reasoningEffort) return selection
  const model = selection.modelIdentifier || profile.modelLabel
  const efforts = supportedEfforts(profile, model)
  if (efforts.length === 0) return selection
  return { modelIdentifier: model, reasoningEffort: efforts.includes(defaultReasoningEffort) ? defaultReasoningEffort : efforts[0] }
}

export function controllerInput(value: string, selection: ModelSelection) {
  return value === 'scripted' ? { type: 'scripted' as const } : { type: 'model' as const, profileId: value, ...selection }
}

export function ControllerSelect({ id, value, onChange, profiles, selection, onSelect }: Readonly<{ id: string; value: string; onChange: (value: string) => void; profiles: ModelProfile[]; selection: ModelSelection; onSelect: (value: ModelSelection) => void }>) {
  const { inspect } = usePermissions()
  const selected = profiles.find(profile => profile.id === value)
  const model = selection.modelIdentifier || selected?.modelLabel || ''
  const options = selected?.modelOptions ?? []
  const efforts = supportedEfforts(selected, model)
  const effort = resolveSelection(selected, selection).reasoningEffort ?? ''
  return (
    <><select id={id} value={value} aria-describedby={`${id}-details`} onChange={(event) => onChange(event.target.value)}>
      <option value="scripted">Scripted · deterministic</option>
      {profiles.filter(profile => profile.available || profile.id === value).map((profile) => {
        return (
        <option key={profile.id} value={profile.id} disabled={!profile.available}>
          {profile.displayName}{!profile.available ? ' · unavailable' : ''}
        </option>
      )})}
    </select><p id={`${id}-details`} className="field-hint">{controllerDetails(value, selected, inspect)}</p>
    {selected && <>
      <label htmlFor={`${id}-model`}>Model</label>
      <select id={`${id}-model`} value={model} onChange={event => onSelect({ modelIdentifier: event.target.value })} disabled={!selected.available}>
        {!options.some(option => option.id === selected.modelLabel) && <option value={selected.modelLabel}>{selected.modelLabel || 'Configured model'}</option>}
        {options.map(option => <option key={option.id} value={option.id}>{option.id}</option>)}
      </select>
      <label htmlFor={`${id}-effort`}>Reasoning effort</label>
      <select id={`${id}-effort`} value={effort} disabled={!selected.available || efforts.length === 0} onChange={event => onSelect({ modelIdentifier: model, reasoningEffort: event.target.value })}>
        {efforts.length === 0 && <option value="">Not configurable</option>}
        {efforts.map(effort => <option key={effort} value={effort}>{effort[0].toUpperCase() + effort.slice(1)}</option>)}
      </select>
      {efforts.length > 0 && <p className="field-hint">Lower effort favors faster decisions. Higher effort allows more deliberation.</p>}
    </>}</>
  )
}

function controllerDetails(value: string, selected: ModelProfile | undefined, inspect: boolean) {
  if (value === 'scripted') return 'Deterministic scripted controller.'
  if (!selected?.available) return 'Selected controller is unavailable. Choose an available controller.'
  if (!inspect) return selected.displayName
  return `${selected.modelLabel || 'Default model'} · ${selected.provider || selected.runtime} · ${selected.scope || 'Execution location unspecified'}`
}
