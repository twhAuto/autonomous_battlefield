import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Faction, FactionApiError, listFactions } from '../api/factions'
import { Emblem } from '../components/Emblem'

export function FactionsPage() {
  const [factions, setFactions] = useState<Faction[]>([])
  const [status, setStatus] = useState<'loading' | 'success' | 'error'>('loading')
  const [message, setMessage] = useState('')
  const [requestVersion, setRequestVersion] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    setStatus('loading')
    setMessage('')
    listFactions(controller.signal)
      .then((result) => {
        setFactions(result)
        setStatus('success')
      })
      .catch((error) => {
        if (controller.signal.aborted) return
        setStatus('error')
        setMessage(error instanceof FactionApiError
          ? error.message
          : 'The faction registry could not be loaded.')
      })
    return () => controller.abort()
  }, [requestVersion])

  return (
    <>
      <section className="page-header page-header--directory">
        <Link className="back-link" to="/">← MAIN MENU</Link>
        <div className="hero__eyebrow"><span>02</span> ACTIVE REGISTRY</div>
        <div className="directory-title">
          <div>
            <h1>All factions</h1>
            <p>Every banner registered on the Autonomous Battlefield.</p>
          </div>
          <Link className="compact-action" to="/factions/new">+ CREATE FACTION</Link>
        </div>
      </section>

      <section className="directory" aria-live="polite" aria-busy={status === 'loading'}>
        <div className="section-label">
          <span>FACTION DIRECTORY</span>
          <span>{status === 'success' ? factions.length.toString().padStart(2, '0') : '--'}</span>
        </div>

        {status === 'loading' && (
          <div className="directory-state">
            <span className="loader" aria-hidden="true" />
            <h2>Contacting the registry</h2>
            <p>Retrieving active faction records…</p>
          </div>
        )}

        {status === 'error' && (
          <div className="directory-state directory-state--error" role="alert">
            <span className="directory-state__code">CONNECTION INTERRUPTED</span>
            <h2>Registry unavailable</h2>
            <p>{message}</p>
            <button className="compact-action" type="button" onClick={() => setRequestVersion((value) => value + 1)}>RETRY CONNECTION</button>
          </div>
        )}

        {status === 'success' && factions.length === 0 && (
          <div className="directory-state">
            <span className="directory-state__code">NO RECORDS FOUND</span>
            <h2>The frontier is unclaimed</h2>
            <p>Create the first faction and establish a presence.</p>
            <Link className="compact-action" to="/factions/new">CREATE FIRST FACTION →</Link>
          </div>
        )}

        {status === 'success' && factions.length > 0 && (
          <div className="faction-grid">
            {factions.map((faction, index) => (
              <article className="registry-card" key={faction.id}>
                <span className="registry-card__index">{(index + 1).toString().padStart(2, '0')}</span>
                <Emblem seed={faction.name} />
                <div className="registry-card__body">
                  <span>REGISTERED FACTION</span>
                  <h2>{faction.name}</h2>
                  <code title={faction.id}>{faction.id}</code>
                </div>
                <div className="registry-card__status"><span className="status-dot" /> ACTIVE</div>
              </article>
            ))}
          </div>
        )}
      </section>
    </>
  )
}

