import { useContext, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { SessionContext } from '../../auth/SessionContext'
import { RolePreviewContext, type PreviewRole } from '../../auth/RolePreviewContext'
import { getModelSettings, saveModelSettings, type AccessPolicy, type AccessRole, type ModelSettings } from '../api/settings'

const roleNames: Record<AccessRole, string> = { admin: 'Administrator', developer: 'Developer', player: 'Player' }
function Switch({ checked, label, disabled, onChange }: Readonly<{ checked: boolean; label: string; disabled: boolean; onChange(): void }>) {
  return <button className="admin-switch" type="button" role="switch" aria-checked={checked} aria-label={label} disabled={disabled} onClick={onChange}>{checked ? 'On' : 'Off'}</button>
}

export function AdminSettingsPage() {
  const session = useContext(SessionContext)
  const preview = useContext(RolePreviewContext)
  const navigate = useNavigate()
  const [settings, setSettings] = useState<ModelSettings | null>(null)
  const [draft, setDraft] = useState<AccessPolicy | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)
  const [refresh, setRefresh] = useState(0)
  const dirty = settings !== null && JSON.stringify(draft) !== JSON.stringify(settings.policy)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true); setError(''); setSaved(false)
    getModelSettings(controller.signal).then(result => {
      if (!controller.signal.aborted) { setSettings(result); setDraft(result.policy) }
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) { setSettings(null); setDraft(null); setError(error instanceof Error ? error.message : 'Settings could not be loaded.') }
    }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [refresh])

  useEffect(() => {
    if (!dirty) return
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = '' }
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [dirty])

  function edit(policy: AccessPolicy) { setDraft(policy); setSaved(false); setError('') }
  async function save() {
    if (!settings || !draft) return
    setSaving(true); setError(''); setSaved(false)
    try {
      const result = await saveModelSettings({ ...settings, policy: draft })
      setSettings({ ...settings, ...result }); setDraft(result.policy); setSaved(true)
    } catch (error: unknown) { setError(error instanceof Error ? error.message : 'Changes could not be saved.') }
    finally { setSaving(false) }
  }
  function enterPreview(role: PreviewRole) {
    preview.setRole(role)
    navigate('/battles/new')
  }

  return <div className="admin-page">
    <header className="admin-heading">
      <span className="hero__eyebrow">SYSTEM / ADMINISTRATION</span><h1>Control center</h1><p>Choose which configured models can be used and who can use them.</p>
    </header>
    <section className="admin-models" aria-labelledby="admin-models-title">
      <div className="admin-section-heading">
        <div><span className="admin-kicker">MODEL ACCESS</span><h2 id="admin-models-title">Models & access</h2><p>Turn a provider or an individual model profile on or off. Changes apply to new battles and background requests; existing battles keep their configuration.</p></div>
        <button className="admin-refresh" type="button" disabled={loading || saving || dirty} onClick={() => setRefresh(value => value + 1)}>Refresh</button>
      </div>
      {loading && <p role="status">Loading model access settings…</p>}
      {error && <p role="alert">{error}</p>}
      {!loading && settings && draft && <>
        {settings.models.length === 0 && <p>No AI model profiles are configured.</p>}
        {Object.keys(draft.providers).map(provider => <section className="admin-provider" key={provider} aria-label={`${provider} access`}>
          <div className="admin-model-heading"><h3>{provider}</h3><Switch label={`${provider} provider access`} checked={draft.providers[provider]} disabled={saving} onChange={() => edit({ ...draft, providers: { ...draft.providers, [provider]: !draft.providers[provider] } })} /></div>
          <div className="admin-model-list">
            {settings.models.filter(model => model.provider === provider).map(model => {
              const access = draft.profiles[model.id]
              return <article className="admin-model" key={model.id}>
                <div className="admin-model-heading"><h4>{model.displayName}</h4><Switch label={`${model.displayName} model access`} checked={access.enabled} disabled={saving || !draft.providers[provider]} onChange={() => edit({ ...draft, profiles: { ...draft.profiles, [model.id]: { ...access, enabled: !access.enabled } } })} /></div>
                <p>{model.modelLabel || 'Default model'} · {model.runtime === 'cli' ? 'CLI subscription' : 'API'}</p>
                <p className={model.available ? 'admin-available' : 'admin-unavailable'}>{!draft.providers[provider] || !access.enabled ? 'Access disabled' : access.allowedRoles.length === 0 ? 'No roles allowed' : model.available ? 'Ready to use' : model.unavailableReason || 'Runtime unavailable'}</p>
                <fieldset disabled={saving || !access.enabled || !draft.providers[provider]} className="admin-roles"><legend>Allowed roles</legend>
                  {(Object.keys(roleNames) as AccessRole[]).map(role => <label key={role}><input type="checkbox" checked={access.allowedRoles.includes(role)} disabled={role === 'player' && model.runtime === 'cli'} onChange={() => edit({ ...draft, profiles: { ...draft.profiles, [model.id]: { ...access, allowedRoles: access.allowedRoles.includes(role) ? access.allowedRoles.filter(value => value !== role) : [...access.allowedRoles, role] } } })} />{roleNames[role]}</label>)}
                </fieldset>
                {model.runtime === 'cli' && <p className="admin-note">CLI subscriptions are limited to administrators and developers.</p>}
              </article>
            })}
          </div>
        </section>)}
        <div className="admin-savebar">
          <span role="status">{saving ? 'Saving…' : saved ? 'Changes saved' : dirty ? 'You have unsaved changes' : 'Settings are up to date'}</span>
          <button type="button" disabled={!dirty || saving} onClick={() => { setDraft(settings.policy); setError(''); setSaved(false) }}>Discard changes</button>
          <button type="button" disabled={!dirty || saving} onClick={() => void save()}>Save changes</button>
        </div>
      </>}
      <p className="admin-note">Enabling access cannot activate missing credentials or a runtime disabled by deployment configuration. The scripted controller remains available without an AI model.</p>
    </section>
    <section className="admin-account" aria-labelledby="admin-preview-title">
      <span className="admin-kicker">ROLE PREVIEW</span><h2 id="admin-preview-title">View as another role</h2>
      <p>Explore the app with that role’s model visibility and inspection permissions, using your own account. Preview is read-only.</p>
      <div className="admin-preview-actions">{(['developer', 'player'] as const).map(role => <button type="button" key={role} disabled={dirty || saving || loading} onClick={() => enterPreview(role)}>View as {roleNames[role]}</button>)}</div>
      {dirty && <p>Save or discard your changes before entering preview or refreshing.</p>}
    </section>
    <section className="admin-account" aria-labelledby="admin-account-title">
      <h2 id="admin-account-title">Your account</h2><p>{session.actor?.email || session.actor?.id || 'local-development'} · Administrator</p>
    </section>
  </div>
}
