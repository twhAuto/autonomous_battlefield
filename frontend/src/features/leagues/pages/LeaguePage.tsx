// One league: progress, champion, running battles, disqualifications, each group's table
// and matches, the knockout bracket and fixtures. Refreshes while the league runs. Admins
// can cancel it or pick an entrant to disqualify, each after confirming, or start a new
// league from a copy of its setup.

import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { cancelLeague, getLeague, LeagueApiError, type League } from '../api/leagues'
import { DisqualifyEntrant } from '../components/DisqualifyEntrant'
import { Disqualifications, FixtureList, GroupStage, KnockoutBracket, NowPlaying } from '../components/LeagueStandings'
import { entrantName } from '../presentation/leagues'

export const leagueRefreshMs = 10_000

export function LeaguePage({ canManage }: Readonly<{ canManage: boolean }>) {
  const { id = '' } = useParams()
  const [league, setLeague] = useState<League | null>(null)
  const [error, setError] = useState('')
  const [version, setVersion] = useState(0)
  const [confirming, setConfirming] = useState(false)
  const [cancelError, setCancelError] = useState('')
  const [disqualifying, setDisqualifying] = useState(false)

  useEffect(() => {
    const controller = new AbortController()
    getLeague(id, controller.signal).then(result => {
      if (!controller.signal.aborted) { setLeague(result); setError('') }
    }).catch((loadError: unknown) => {
      if (!controller.signal.aborted) setError(loadError instanceof LeagueApiError ? loadError.message : 'The league could not be loaded.')
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
    } catch (requestError) {
      setCancelError(requestError instanceof LeagueApiError ? requestError.message : 'The league could not be cancelled.')
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
            {canManage && <Link className="compact-action" to={`/leagues/new?from=${encodeURIComponent(league.id)}`}>START AGAIN →</Link>}
            {canManage && running && <button className="compact-action" type="button" aria-expanded={disqualifying} aria-controls="league-disqualify"
              onClick={() => setDisqualifying(open => !open)}>DISQUALIFY ENTRANT</button>}
            {canManage && running && !confirming && <button className="compact-action" type="button" onClick={() => setConfirming(true)}>CANCEL LEAGUE</button>}
            {canManage && running && confirming && <><button className="compact-action compact-action--danger" type="button" onClick={cancel}>CONFIRM CANCEL</button>
              <button className="compact-action" type="button" onClick={() => setConfirming(false)}>KEEP RUNNING</button></>}
          </div>
        </div>
        {cancelError && <p className="field-error" role="alert">{cancelError}</p>}
        {canManage && running && disqualifying && <DisqualifyEntrant id="league-disqualify" league={league}
          onDone={updated => { setLeague(updated); setDisqualifying(false) }} onClose={() => setDisqualifying(false)} />}
        {error && <output className="field-error league-refresh-error">Refresh failed: {error}</output>}
      </section>
      {league.champion !== null && <output className="league-champion">Champion: <strong>{entrantName(league, league.champion)}</strong></output>}
      <progress className="battle-progress" max={league.progress.total} value={league.progress.settled} aria-label="League progress" />
      <NowPlaying league={league} />
      <Disqualifications league={league} />
      <GroupStage league={league} />
      <KnockoutBracket league={league} />
      <FixtureList league={league} />
    </>
  )
}
