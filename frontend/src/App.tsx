import { FormEvent, useMemo, useState } from 'react'
import { createFaction, FactionApiError } from './api/factions'
import { Emblem } from './components/Emblem'
import './styles.css'

interface CreatedFaction {
  id: string
  name: string
  createdAt: Date
}

function newFactionId() {
  return crypto.randomUUID()
}

function shortId(id: string) {
  return `${id.slice(0, 8)}…${id.slice(-4)}`
}

export default function App() {
  const [name, setName] = useState('')
  const [factionId, setFactionId] = useState(newFactionId)
  const [createdFactions, setCreatedFactions] = useState<CreatedFaction[]>([])
  const [status, setStatus] = useState<'idle' | 'submitting' | 'success' | 'error'>('idle')
  const [message, setMessage] = useState('')

  const trimmedName = name.trim()
  const previewName = trimmedName || 'Your faction'
  const canSubmit = trimmedName.length > 0 && status !== 'submitting'
  const currentDate = useMemo(
    () => new Intl.DateTimeFormat('en', { month: 'short', day: '2-digit', year: 'numeric' }).format(new Date()),
    [],
  )

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
      setCreatedFactions((factions) => [
        { id: factionId, name: trimmedName, createdAt: new Date() },
        ...factions,
      ])
      setStatus('success')
      setMessage(`${trimmedName} has entered the battlefield.`)
      setName('')
      setFactionId(newFactionId())
    } catch (error) {
      setStatus('error')
      setMessage(
        error instanceof FactionApiError
          ? error.message
          : 'An unexpected error occurred. Please try again.',
      )
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
    <div className="app-shell">
      <header className="topbar">
        <a className="brand" href="#top" aria-label="Autonomous Battlefield home">
          <span className="brand__glyph">A</span>
          <span>AUTONOMOUS<br />BATTLEFIELD</span>
        </a>
        <div className="topbar__status">
          <span className="status-dot" />
          FRONTIER ONLINE
          <span className="topbar__divider" />
          {currentDate.toUpperCase()}
        </div>
      </header>

      <main id="top">
        <section className="hero">
          <div className="hero__eyebrow"><span>01</span> FACTION REGISTRY</div>
          <h1>Stake your claim.<br /><em>Shape the frontier.</em></h1>
          <p className="hero__copy">
            Forge an identity, raise your banner, and enter a world where every
            decision redraws the map.
          </p>
          <div className="hero__coordinates">47°12' N &nbsp; / &nbsp; SECTOR 09</div>
        </section>

        <section className="creation-layout" aria-labelledby="creation-heading">
          <div className="form-panel">
            <div className="section-label"><span>NEW DEPLOYMENT</span><span>STEP 1 OF 1</span></div>
            <h2 id="creation-heading">Create a faction</h2>
            <p className="form-panel__intro">
              Choose a name that allies will rally behind—and rivals will learn to fear.
            </p>

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
                <button type="button" className="icon-button" onClick={regenerateId} aria-label="Generate a new faction identifier">
                  ↻
                </button>
              </div>

              <button className="submit-button" type="submit" disabled={!canSubmit}>
                <span>{status === 'submitting' ? 'DEPLOYING…' : 'CREATE FACTION'}</span>
                <span aria-hidden="true">→</span>
              </button>

              <div className="form-message" aria-live="polite">
                {message && <p className={`form-message--${status}`}>{message}</p>}
              </div>
            </form>
          </div>

          <aside className="preview-panel" aria-label="Faction identity preview">
            <div className="preview-panel__grid" />
            <div className="preview-panel__heading">
              <span>IDENTITY PREVIEW</span><span>LIVE</span>
            </div>
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

        {createdFactions.length > 0 && (
          <section className="recent" aria-labelledby="recent-heading">
            <div className="section-label"><span>ACTIVE ROSTER</span><span>{createdFactions.length.toString().padStart(2, '0')}</span></div>
            <h2 id="recent-heading">Recently deployed</h2>
            <div className="recent__list">
              {createdFactions.map((faction) => (
                <article key={faction.id} className="faction-card">
                  <Emblem seed={faction.name} size="small" />
                  <div>
                    <h3>{faction.name}</h3>
                    <p title={faction.id}>{shortId(faction.id)}</p>
                  </div>
                  <time dateTime={faction.createdAt.toISOString()}>
                    {faction.createdAt.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
                  </time>
                </article>
              ))}
            </div>
          </section>
        )}
      </main>

      <footer>
        <span>AUTONOMOUS BATTLEFIELD / COMMAND INTERFACE</span>
        <span>BUILD 0.1.0</span>
      </footer>
    </div>
  )
}

