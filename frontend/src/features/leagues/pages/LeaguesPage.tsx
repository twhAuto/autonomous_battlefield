// League registry for Testers and Admins: status, progress and champion, with the
// league setup link for Admins.

import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { listLeagues, type LeagueSummary } from '../api/leagues'

export function LeaguesPage({ canCreate }: Readonly<{ canCreate: boolean }>) {
  const [leagues, setLeagues] = useState<LeagueSummary[] | null>(null)
  const [error, setError] = useState('')
  const [version, setVersion] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    setError('')
    listLeagues(controller.signal).then((result) => {
      if (!controller.signal.aborted) setLeagues(result.leagues)
    }).catch((loadError: unknown) => {
      if (!controller.signal.aborted) setError(loadError instanceof Error ? loadError.message : 'Leagues could not be loaded.')
    })
    return () => controller.abort()
  }, [version])

  return (
    <>
      <section className="page-header page-header--directory">
        <Link className="back-link" to="/">← MAIN MENU</Link>
        <div className="hero__eyebrow"><span>05</span> LEAGUES</div>
        <div className="directory-title">
          <div><h1>Leagues</h1><p>AI tournaments played in the background: a dual tournament in each group of four, then a crossover knockout, one battle per match.</p></div>
          {canCreate && <Link className="compact-action" to="/leagues/new">+ START LEAGUE</Link>}
        </div>
      </section>
      <section className="directory" aria-label="Leagues" aria-busy={leagues === null && !error}>
        {leagues === null && !error && <output>Loading leagues…</output>}
        {error && <div role="alert"><p>{error}</p><button className="compact-action" type="button" onClick={() => setVersion((value) => value + 1)}>RETRY</button></div>}
        {leagues?.length === 0 && <div className="directory-state">
          <h2>No leagues yet</h2>
          <p>{canCreate ? 'Start a league to compare models, factions and units across many battles.' : 'An administrator can start a league.'}</p>
          {canCreate && <Link className="compact-action" to="/leagues/new">START LEAGUE →</Link>}
        </div>}
        {leagues && leagues.length > 0 && <table className="history-table">
          <caption className="visually-hidden">Leagues, newest first</caption>
          <thead><tr><th>League</th><th>Created</th><th>Status</th><th>Progress</th><th>Champion</th><th><span className="visually-hidden">Details</span></th></tr></thead>
          <tbody>{leagues.map((league) => <tr key={league.id}>
            <td data-label="League"><strong>{league.name}</strong><small>{league.entrantCount} entrants · {league.groupCount} groups</small></td>
            <td data-label="Created"><time dateTime={league.createdAt}>{new Date(league.createdAt).toLocaleString()}</time></td>
            <td data-label="Status"><span className="history-status">{league.status}</span></td>
            <td data-label="Progress">{league.progress.settled} / {league.progress.total} fixtures</td>
            <td data-label="Champion">{league.champion?.factionName ?? '—'}</td>
            <td><Link className="text-link" to={`/leagues/${encodeURIComponent(league.id)}`}>Open league →</Link></td>
          </tr>)}</tbody>
        </table>}
      </section>
    </>
  )
}
