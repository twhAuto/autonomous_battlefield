// One league: progress, champion, group tables, knockout bracket and fixtures. Refreshes
// while the league runs; Admins can cancel it after confirming.

import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { cancelLeague, getLeague, LeagueApiError, type League } from '../api/leagues'
import { FixtureList, GroupTables, KnockoutBracket } from '../components/LeagueStandings'
import { entrantName } from '../presentation/leagues'

export const leagueRefreshMs = 10_000

export function LeaguePage({ canCancel }: Readonly<{ canCancel: boolean }>) {
  const { id = '' } = useParams()
  const [league, setLeague] = useState<League | null>(null)
  const [error, setError] = useState('')
  const [version, setVersion] = useState(0)
  const [confirming, setConfirming] = useState(false)
  const [cancelError, setCancelError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    getLeague(id, controller.signal).then(result => {
      if (!controller.signal.aborted) { setLeague(result); setError('') }
    }).catch((failure: unknown) => {
      if (!controller.signal.aborted) setError(failure instanceof LeagueApiError ? failure.message : 'The league could not be loaded.')
    })
    return () => controller.abort()
  }, [id, version])

  const running = league?.status === 'running'
  useEffect(() => {
    if (!running) return
    const timer = window.setInterval(() => setVersion(value => value + 1), leagueRefreshMs)
    return () => window.clearInterval(timer)
  }, [running])

  async function cancel() {
    setCancelError('')
    try {
      setLeague(await cancelLeague(id))
      setConfirming(false)
    } catch (failure) {
      setCancelError(failure instanceof LeagueApiError ? failure.message : 'The league could not be cancelled.')
    }
  }

  if (!league) {
    return <section className="directory-state" aria-busy={!error}>
      {error ? <div role="alert"><h2>League unavailable</h2><p>{error}</p><button className="compact-action" type="button" onClick={() => setVersion(value => value + 1)}>RETRY</button></div>
        : <><span className="loader" aria-hidden="true" /><h2>Loading league…</h2></>}
    </section>
  }
  return (
    <>
      <section className="page-header page-header--directory">
        <Link className="back-link" to="/leagues">← LEAGUES</Link>
        <div className="hero__eyebrow"><span>05</span> LEAGUE · {league.status.toUpperCase()}</div>
        <div className="directory-title">
          <div>
            <h1>{league.name}</h1>
            <p>{league.entrants.length} entrants · {league.settings.scenarioIds.join(', ')} · {league.settings.tickCount} ticks per battle · {league.progress.settled} of {league.progress.total} fixtures settled</p>
          </div>
          <div className="league-actions">
            <Link className="compact-action" to={`/analytics?leagueId=${encodeURIComponent(league.id)}`}>STATISTICS →</Link>
            {canCancel && running && !confirming && <button className="compact-action" type="button" onClick={() => setConfirming(true)}>CANCEL LEAGUE</button>}
            {canCancel && running && confirming && <><button className="compact-action compact-action--danger" type="button" onClick={cancel}>CONFIRM CANCEL</button>
              <button className="compact-action" type="button" onClick={() => setConfirming(false)}>KEEP RUNNING</button></>}
          </div>
        </div>
        {cancelError && <p className="field-error" role="alert">{cancelError}</p>}
        {error && <p className="field-error" role="status">Refresh failed: {error}</p>}
      </section>
      {league.champion !== null && <output className="league-champion">Champion: <strong>{entrantName(league, league.champion)}</strong></output>}
      <progress className="battle-progress" max={league.progress.total} value={league.progress.settled} aria-label="League progress" />
      <GroupTables league={league} />
      <KnockoutBracket league={league} />
      <FixtureList league={league} />
    </>
  )
}
