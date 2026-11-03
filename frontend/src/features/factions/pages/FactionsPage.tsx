// Loads the faction registry with retry states and links to the authorized editor. A
// Player who owns a faction sees why they cannot create another instead of the action.

import { usePermissions } from '../../auth/SessionContext'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Faction, FactionApiError, listFactions } from '../api/factions'
import { Emblem } from '../../../shared/components/Emblem'
import { factionLimitMessage } from '../limits'

export function FactionsPage() {
 const permissions = usePermissions()
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

  const atLimit = status === 'success' && permissions.atFactionLimit(factions)
  // A Player's action waits for the registry; if it cannot load, the API still enforces the limit.
  const canCreate = permissions.play && !atLimit && !(permissions.oneFaction && status === 'loading')

  return (
    <>
      <section className="page-header page-header--directory">
        <Link className="back-link" to="/">← MAIN MENU</Link>
        <div className="hero__eyebrow"><span>02</span> ACTIVE REGISTRY</div>
        <div className="directory-title">
          <div>
            <h1>Factions</h1>
            <p>Every banner registered in OrdinalFrontier.</p>
          </div>
          {canCreate && <Link className="compact-action" to="/factions/new">+ CREATE FACTION</Link>}
          {atLimit && <p className="faction-limit" role="note">{factionLimitMessage}</p>}
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
            <p>{permissions.play ? 'Create the first faction and establish a presence.' : 'No factions have been registered yet.'}</p>
            {permissions.play && <Link className="compact-action" to="/factions/new">CREATE FIRST FACTION →</Link>}
          </div>
        )}

        {status === 'success' && factions.length > 0 && (
          <div className="faction-grid">
            {factions.map((faction, index) => (
              <article className="registry-card" key={faction.id}>
                <span className="registry-card__index">{(index + 1).toString().padStart(2, '0')}</span>
                <Emblem seed={faction.name} emblem={faction.emblem} />
                <div className="registry-card__body">
                  <span>REGISTERED FACTION</span>
                  <h2>{faction.name}</h2>
                  {faction.ownerName && <p className="registry-card__owner">Created by {faction.ownerName}</p>}
                  <p style={{ whiteSpace: 'pre-wrap' }}>{faction.doctrine}</p>
                  {faction.background && <p style={{ whiteSpace: 'pre-wrap' }}>{faction.background}</p>}
                  {permissions.canManage(faction.ownerId) && <Link to={`/factions/${faction.id}/edit`}>Edit faction</Link>}
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
