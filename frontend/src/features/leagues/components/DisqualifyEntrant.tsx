// Picks an entrant to disqualify and confirms it with an optional reason, then hands back
// the league the server returns. A disqualified entrant loses the match it is playing, and
// every match it would play later goes to its opponent.

import { useState, type SubmitEvent } from 'react'
import { disqualifyEntrant, LeagueApiError, type League } from '../api/leagues'
import { controllerName } from '../../../shared/domain/controllers'
import { entrantName } from '../presentation/leagues'

export const maxReasonLength = 200

// Entrants that can still win and are not yet disqualified, by group in table order.
function candidates(league: League) {
  return league.groups.map(({ group, standings }) => ({
    group,
    entrants: standings.flatMap(({ entrant: index }) => {
      const entrant = league.entrants[index]
      return entrant && !entrant.eliminated && !entrant.disqualification ? [entrant] : []
    }),
  })).filter(({ entrants }) => entrants.length > 0)
}

export function DisqualifyEntrant({ id, league, onDone, onClose }: Readonly<{ id: string; league: League; onDone: (league: League) => void; onClose: () => void }>) {
  const [picked, setPicked] = useState('')
  const [reason, setReason] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const groups = candidates(league)
  // A refresh can eliminate the picked entrant; it is no longer offered then.
  const chosen = groups.flatMap(({ entrants }) => entrants).find(({ index }) => String(index) === picked)?.index ?? null

  async function submit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (chosen === null) return
    setBusy(true)
    setError('')
    try {
      onDone(await disqualifyEntrant(league.id, chosen, reason.trim()))
    } catch (requestError) {
      setError(requestError instanceof LeagueApiError ? requestError.message : 'The entrant could not be disqualified.')
      setBusy(false)
    }
  }

  return (
    <form id={id} className="league-disqualify" aria-label="Disqualify an entrant" onSubmit={submit}>
      <label>Entrant<select value={chosen === null ? '' : String(chosen)} required onChange={event => { setPicked(event.target.value); setError('') }}>
        <option value="" disabled>Choose an entrant</option>
        {groups.map(({ group, entrants }) => <optgroup key={group} label={`Group ${group}`}>
          {entrants.map(({ index, controller }) => <option key={index} value={index}>{entrantName(league, index)} ({controllerName(controller)})</option>)}
        </optgroup>)}
      </select></label>
      <label>Reason (optional)<input type="text" value={reason} maxLength={maxReasonLength} onChange={event => setReason(event.target.value)} /></label>
      <p>{chosen === null ? 'Choose the entrant to disqualify.' : <>Disqualify <strong>{entrantName(league, chosen)}</strong>?</>} It loses the match it is playing, and every match it would play later goes to its opponent.</p>
      <div className="league-actions">
        <button className="compact-action compact-action--danger" type="submit" disabled={busy || chosen === null}>CONFIRM DISQUALIFY</button>
        <button className="compact-action" type="button" onClick={onClose}>KEEP IN LEAGUE</button>
      </div>
      {error && <p className="field-error" role="alert">{error}</p>}
    </form>
  )
}
