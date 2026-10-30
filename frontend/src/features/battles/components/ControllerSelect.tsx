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

/** The selected model and effort the profile no longer offers ('' when offered). League setup copies selections that can go stale. */
export function staleSelection(profile: ModelProfile, selection: ModelSelection) {
  const model = selection.modelIdentifier || profile.modelLabel
  const offered = model === profile.modelLabel || (profile.modelOptions ?? []).some(option => option.id === model)
  const effort = selection.reasoningEffort ?? ''
  return { model: offered ? '' : model, effort: supportedEfforts(profile, model).includes(effort) ? '' : effort }
}

const titleCase = (text: string) => text[0].toUpperCase() + text.slice(1)

// Provider API keys and subscription CLIs are listed separately.
const runtimeGroups = [['api', 'API'], ['cli', 'CLI']] as const

export function controllerInput(value: string, selection: ModelSelection) {
  return value === 'scripted' ? { type: 'scripted' as const } : { type: 'model' as const, profileId: value, ...selection }
}

export function ControllerSelect({ id, value, onChange, profiles, selection, onSelect }: Readonly<{ id: string; value: string; onChange: (value: string) => void; profiles: ModelProfile[]; selection: ModelSelection; onSelect: (value: ModelSelection) => void }>) {
  const { inspect } = usePermissions()
  const selected = profiles.find(profile => profile.id === value)
  return (
    <><select id={id} value={value} aria-describedby={`${id}-details`} onChange={(event) => onChange(event.target.value)}>
      <option value="scripted">Scripted · deterministic</option>
      {/* A copied selection whose profile is no longer listed stays visible instead of reading as Scripted. */}
      {value !== 'scripted' && !selected && <option value={value} disabled>Unavailable controller</option>}
      {runtimeGroups.map(([runtime, label]) => {
        const listed = profiles.filter(profile => profile.runtime === runtime && (profile.available || profile.id === value))
        return listed.length > 0 && (
          <optgroup key={runtime} label={label}>
            {listed.map(profile => (
              <option key={profile.id} value={profile.id} disabled={!profile.available}>
                {profile.displayName}{!profile.available ? ' · unavailable' : ''}
              </option>
            ))}
          </optgroup>
        )
      })}
    </select><p id={`${id}-details`} className="field-hint">{controllerDetails(value, selected, inspect)}</p>
    {selected && <ModelFields id={id} profile={selected} selection={selection} onSelect={onSelect} />}</>
  )
}

// A stale model or effort stays selected as an unavailable option, so what shows is
// what would be submitted.
function ModelFields({ id, profile, selection, onSelect }: Readonly<{ id: string; profile: ModelProfile; selection: ModelSelection; onSelect: (value: ModelSelection) => void }>) {
  const model = selection.modelIdentifier || profile.modelLabel
  const options = profile.modelOptions ?? []
  const efforts = supportedEfforts(profile, model)
  const effort = resolveSelection(profile, selection).reasoningEffort ?? ''
  const stale = staleSelection(profile, selection)
  return (
    <>
      <label htmlFor={`${id}-model`}>Model</label>
      <select id={`${id}-model`} value={model} onChange={event => onSelect({ modelIdentifier: event.target.value })} disabled={!profile.available}>
        {!options.some(option => option.id === profile.modelLabel) && <option value={profile.modelLabel}>{profile.modelLabel || 'Configured model'}</option>}
        {options.map(option => <option key={option.id} value={option.id}>{option.id}</option>)}
        {stale.model && <option value={stale.model} disabled>{stale.model} · unavailable</option>}
      </select>
      <label htmlFor={`${id}-effort`}>Reasoning effort</label>
      <select id={`${id}-effort`} value={effort} disabled={!profile.available || (efforts.length === 0 && !stale.effort)} onChange={event => onSelect({ modelIdentifier: model, reasoningEffort: event.target.value })}>
        {efforts.length === 0 && <option value="">Not configurable</option>}
        {efforts.map(effort => <option key={effort} value={effort}>{titleCase(effort)}</option>)}
        {stale.effort && <option value={stale.effort} disabled>{titleCase(stale.effort)} · unavailable</option>}
      </select>
      {efforts.length > 0 && <p className="field-hint">Lower effort favors faster decisions. Higher effort allows more deliberation.</p>}
    </>
  )
}

function controllerDetails(value: string, selected: ModelProfile | undefined, inspect: boolean) {
  if (value === 'scripted') return 'Deterministic scripted controller.'
  if (!selected?.available) return 'Selected controller is unavailable. Choose an available controller.'
  if (!inspect) return selected.displayName
  return `${selected.modelLabel || 'Default model'} · ${selected.provider || selected.runtime} · ${selected.scope || 'Execution location unspecified'}`
}
