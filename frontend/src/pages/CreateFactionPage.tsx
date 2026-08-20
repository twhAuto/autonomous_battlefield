import { FormEvent, useState } from 'react'
import { Link } from 'react-router-dom'
import { createFaction, FactionApiError } from '../api/factions'
import { Emblem } from '../components/Emblem'

function newFactionId() {
  return crypto.randomUUID()
}

export function CreateFactionPage() {
  const [name, setName] = useState('')
  const [factionId, setFactionId] = useState(newFactionId)
  const [status, setStatus] = useState<'idle' | 'submitting' | 'success' | 'error'>('idle')
  const [message, setMessage] = useState('')

  const trimmedName = name.trim()
  const previewName = trimmedName || 'Your faction'
  const canSubmit = trimmedName.length > 0 && status !== 'submitting'

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!trimmedName) {
      setStatus('error')
      setMessage('Give your faction a name before deploying it.')
      return
    }

    setStatus('submitting')
    setMessage('')
    try {
      await createFaction({ id: factionId, name: trimmedName })
      setStatus('success')
      setMessage(`${trimmedName} has entered the battlefield.`)
      setName('')
      setFactionId(newFactionId())
    } catch (error) {
      setStatus('error')
      setMessage(error instanceof FactionApiError
        ? error.message
        : 'An unexpected error occurred. Please try again.')
    }
  }

  function regenerateId() {
    setFactionId(newFactionId())
    if (status !== 'submitting') {
      setStatus('idle')
      setMessage('')
    }
  }

  return (
    <>
      <section className="page-header">
        <Link className="back-link" to="/">← MAIN MENU</Link>
        <div className="hero__eyebrow"><span>01</span> FACTION REGISTRY</div>
        <h1>Create a faction</h1>
        <p>Forge an identity that allies will rally behind—and rivals will learn to fear.</p>
      </section>

      <section className="creation-layout" aria-labelledby="creation-heading">
        <div className="form-panel">
          <div className="section-label"><span>NEW DEPLOYMENT</span><span>STEP 1 OF 1</span></div>
          <h2 id="creation-heading">Faction identity</h2>
          <p className="form-panel__intro">Choose a designation for the official battlefield registry.</p>

          <form onSubmit={handleSubmit} noValidate>
            <label htmlFor="faction-name">Faction name</label>
            <div className="name-field">
              <input
                id="faction-name"
                name="factionName"
                value={name}
                onChange={(event) => {
                  setName(event.target.value)
                  if (status !== 'submitting') {
                    setStatus('idle')
                    setMessage('')
                  }
                }}
                placeholder="e.g. The Verdant Accord"
                autoComplete="off"
                autoFocus
                aria-describedby="name-hint"
                aria-invalid={status === 'error'}
              />
              <span>{name.length}</span>
            </div>
            <p id="name-hint" className="field-hint">Spaces at either end will be removed.</p>

            <div className="identity-block">
              <div>
                <span className="identity-block__label">SYSTEM IDENTIFIER</span>
                <code title={factionId}>{factionId}</code>
              </div>
              <button type="button" className="icon-button" onClick={regenerateId} aria-label="Generate a new faction identifier">↻</button>
            </div>

            <button className="submit-button" type="submit" disabled={!canSubmit}>
              <span>{status === 'submitting' ? 'DEPLOYING…' : 'CREATE FACTION'}</span>
              <span aria-hidden="true">→</span>
            </button>

            <div className="form-message" aria-live="polite">
              {message && <p className={`form-message--${status}`}>{message}</p>}
              {status === 'success' && <Link className="text-link" to="/factions">View faction registry →</Link>}
            </div>
          </form>
        </div>

        <aside className="preview-panel" aria-label="Faction identity preview">
          <div className="preview-panel__grid" />
          <div className="preview-panel__heading"><span>IDENTITY PREVIEW</span><span>LIVE</span></div>
          <div className="preview-panel__content">
            <Emblem seed={previewName} />
            <span className="preview-panel__caption">DESIGNATION</span>
            <h3>{previewName}</h3>
            <div className="preview-panel__rule" />
            <div className="preview-panel__meta">
              <div><span>STATUS</span><strong>UNDEPLOYED</strong></div>
              <div><span>SECTOR</span><strong>UNASSIGNED</strong></div>
            </div>
          </div>
          <p className="preview-panel__footnote">Your banner adapts to your chosen designation.</p>
        </aside>
      </section>
    </>
  )
}

