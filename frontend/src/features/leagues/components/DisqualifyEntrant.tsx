// Confirms an entrant's disqualification with an optional reason, then hands back the
// league the server returns. A disqualified entrant loses the match it is playing, and
// every match it would play later goes to its opponent.

import { useState, type FormEvent } from 'react'
import { disqualifyEntrant, LeagueApiError, type League } from '../api/leagues'
import { entrantName } from '../presentation/leagues'

export const maxReasonLength = 200

export function DisqualifyEntrant({ league, entrant, onDone, onClose }: Readonly<{ league: League; entrant: number; onDone: (league: League) => void; onClose: () => void }>) {
  const [reason, setReason] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const name = entrantName(league, entrant)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      onDone(await disqualifyEntrant(league.id, entrant, reason.trim()))
    } catch (requestError) {
      setError(requestError instanceof LeagueApiError ? requestError.message : 'The entrant could not be disqualified.')
      setBusy(false)
    }
  }

  return (
    <form className="league-disqualify" aria-label={`Disqualify ${name}`} onSubmit={submit}>
      <p>Disqualify <strong>{name}</strong>? It loses the match it is playing, and every match it would play later goes to its opponent.</p>
      <label>Reason (optional)<input type="text" value={reason} maxLength={maxReasonLength} onChange={event => setReason(event.target.value)} /></label>
      <div className="league-actions">
        <button className="compact-action compact-action--danger" type="submit" disabled={busy}>CONFIRM DISQUALIFY</button>
        <button className="compact-action" type="button" onClick={onClose}>KEEP IN LEAGUE</button>
      </div>
      {error && <p className="field-error" role="alert">{error}</p>}
    </form>
  )
}
