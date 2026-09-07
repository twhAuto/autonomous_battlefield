// Battle registry with URL-backed status filters, cursor navigation, refresh/retry states,
// and links that preserve the return location.

import { useEffect, useState } from 'react'
import { Link, useLocation, useSearchParams } from 'react-router-dom'
import { listBattles, type BattleHistoryPage as HistoryPage, type BattleSummary } from '../api/battles'

const filters = ['', 'queued', 'running', 'paused', 'completed', 'failed', 'stopped']
function outcomeLabel(battle: BattleSummary) {
  if (battle.status !== 'completed' || !battle.outcome) return '—'
  const winner = battle.outcome.winnerFactionId
  return winner
    ? `${battle.participants.find((participant) => participant.id === winner)?.name ?? winner} wins`
    : 'Draw'
}

export function BattleHistoryPage() {
  const [params, setParams] = useSearchParams()
  const location = useLocation()
  const status = params.get('status') ?? ''
  const cursor = params.get('cursor') ?? ''
  const previous = params.getAll('previous')
  const [page, setPage] = useState<HistoryPage | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [version, setVersion] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    setPage(null)
    listBattles({ status, cursor }, controller.signal).then((result) => {
      if (!controller.signal.aborted) setPage(result)
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setError(error instanceof Error ? error.message : 'Battle history could not be loaded.')
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false)
    })
    return () => controller.abort()
  }, [status, cursor, version])

  function firstPage(nextStatus: string) {
    setParams(nextStatus ? { status: nextStatus } : {})
  }
  function refresh() {
    firstPage(status)
    setVersion((value) => value + 1)
  }
  function nextPage() {
    if (!page?.nextCursor) return
    const next = new URLSearchParams(params)
    next.append('previous', cursor)
    next.set('cursor', page.nextCursor)
    setParams(next)
  }
  function previousPage() {
    const next = new URLSearchParams(params)
    next.delete('previous')
    previous.slice(0, -1).forEach((value) => next.append('previous', value))
    const prior = previous.at(-1)
    if (prior) next.set('cursor', prior)
    else next.delete('cursor')
    setParams(next)
  }

  return (
    <>
      <section className="page-header page-header--directory">
        <Link className="back-link" to="/">← MAIN MENU</Link>
        <div className="hero__eyebrow"><span>04</span> ENGAGEMENT ARCHIVE</div>
        <div className="directory-title">
          <div><h1>Battle history</h1><p>Review past engagements and return to active battles.</p></div>
          <Link className="compact-action" to="/battles/new">+ STAGE BATTLE</Link>
        </div>
      </section>
      <section className="directory" aria-label="Battle history" aria-busy={loading}>
        <div className="history-toolbar">
          <label>Status <select value={status} onChange={(event) => firstPage(event.target.value)}>
            {!filters.includes(status) && <option value={status}>Unknown status</option>}
            {filters.map((value) => <option key={value} value={value}>{value ? value[0].toUpperCase() + value.slice(1) : 'All'}</option>)}
          </select></label>
          <button className="compact-action" type="button" onClick={refresh} disabled={loading}>REFRESH</button>
        </div>
        {loading && <p role="status">Loading battle history…</p>}
        {error && <div role="alert"><p>{error}</p><button className="compact-action" onClick={() => setVersion((value) => value + 1)}>RETRY</button></div>}
        {!loading && page && <>
          {page.battles.length === 0 ? <div className="directory-state">
            <h2>{status || cursor ? 'No matching battles' : 'No battles yet'}</h2>
            <p>{status || cursor ? 'Change the status filter or refresh to see the latest battles.' : 'Stage your first battle to begin the archive.'}</p>
            {!status && !cursor && <Link className="compact-action" to="/battles/new">STAGE BATTLE →</Link>}
          </div> : <table className="history-table">
            <caption className="visually-hidden">Battles, newest first</caption>
            <thead><tr><th>Battle</th><th>Created</th><th>Status</th><th>Progress</th><th>Outcome</th><th><span className="visually-hidden">Details</span></th></tr></thead>
            <tbody>{page.battles.map((battle) => <tr key={battle.id}>
              <td data-label="Battle"><strong>{battle.participants.map((participant) => participant.name).join(' vs ')}</strong><small>{battle.scenarioId}</small><code title={battle.id}>{battle.id}</code></td>
              <td data-label="Created"><time dateTime={battle.createdAt}>{new Date(battle.createdAt).toLocaleString()}</time></td>
              <td data-label="Status"><span className="history-status">{battle.paused && (battle.status === 'queued' || battle.status === 'running') ? 'Paused' : battle.status}</span></td>
              <td data-label="Progress">{battle.currentTick} / {battle.requestedTicks} ticks</td>
              <td data-label="Outcome">{outcomeLabel(battle)}</td>
              <td><Link className="text-link" to={`/battles/${encodeURIComponent(battle.id)}`} state={{ historyURL: location.pathname + location.search }}>Open battle →</Link></td>
            </tr>)}</tbody>
          </table>}
          <nav className="history-pagination" aria-label="Battle history pages">
            <button className="compact-action" type="button" disabled={!cursor} onClick={previousPage}>← PREVIOUS</button>
            <button className="compact-action" type="button" disabled={!page.nextCursor} onClick={nextPage}>NEXT →</button>
          </nav>
        </>}
      </section>
    </>
  )
}
