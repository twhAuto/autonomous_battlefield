// Display-name form shared by the first-sign-in prompt and the profile page. It checks
// the rules before sending and shows the server's reason when a save fails.

import { type SubmitEvent, useId, useState } from 'react'
import { displayNameHint, normalizeDisplayName, ProfileApiError, setDisplayName } from '../api/profile'

interface DisplayNameFormProps {
  readonly initialName?: string
  readonly submitLabel: string
  readonly onSaved: (name: string) => void
}

export function DisplayNameForm({ initialName = '', submitLabel, onSaved }: DisplayNameFormProps) {
  const id = useId()
  const [name, setName] = useState(initialName)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  async function submit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    const normalized = normalizeDisplayName(name)
    if (!normalized) {
      setError(displayNameHint)
      return
    }
    setSaving(true)
    setError('')
    try {
      const saved = await setDisplayName(normalized)
      setName(saved)
      onSaved(saved)
    } catch (saveError) {
      setError(saveError instanceof ProfileApiError ? saveError.message : 'Your display name could not be saved. Try again.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <form className="display-name-form" onSubmit={submit} noValidate>
      <label htmlFor={`${id}-name`}>Display name</label>
      <div className="name-field">
        <input
          id={`${id}-name`}
          autoComplete="nickname"
          value={name}
          onChange={(event) => setName(event.target.value)}
          aria-describedby={error ? `${id}-hint ${id}-error` : `${id}-hint`}
          aria-invalid={error ? true : undefined}
          required
        />
      </div>
      <p id={`${id}-hint`} className="field-hint">{displayNameHint} Everyone sees it on the battles you start and the factions you create.</p>
      {error && <p id={`${id}-error`} className="form-message--error" role="alert">{error}</p>}
      <button className="submit-button" type="submit" disabled={saving}>
        <span>{saving ? 'SAVING…' : submitLabel}</span>
        <span>→</span>
      </button>
    </form>
  )
}
