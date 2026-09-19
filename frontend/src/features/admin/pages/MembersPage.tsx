import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { getMembers, grantMember, removeMember, retryMembers, type Members, type Member } from '../api/members'

function accessLabel(member: Member) {
  if (!member.desiredAccess) return 'Removed'
  return member.version === member.syncedVersion ? 'Ready to play' : 'Waiting for access'
}
function message(error: unknown) { return error instanceof Error ? error.message : 'The request failed. Please try again.' }

export function MembersPage() {
  const [data, setData] = useState<Members | null>(null)
  const [email, setEmail] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [loadError, setLoadError] = useState('')
  const [notice, setNotice] = useState('')
  const [refresh, setRefresh] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    // Poll after completion rather than overlapping slow requests.
    let timer: ReturnType<typeof setTimeout>
    async function load() {
      try {
        const next = await getMembers(controller.signal)
        if (!controller.signal.aborted) { setData(next); setLoadError('') }
      } catch (error: unknown) { if (!controller.signal.aborted) setLoadError(message(error)) }
      finally { if (!controller.signal.aborted) timer = setTimeout(() => { void load() }, 5000) }
    }
    void load()
    return () => { controller.abort(); clearTimeout(timer) }
  }, [refresh])

  async function change(action: () => Promise<void>, success: string) {
    setBusy(true); setError(''); setNotice('')
    try { await action(); setNotice(success); setRefresh(value => value + 1) }
    catch (error: unknown) { setError(message(error)) }
    finally { setBusy(false) }
  }
  async function copyLink() {
    if (!data) return
    try { await navigator.clipboard.writeText(data.gameUrl); setNotice('Game link copied.') }
    catch { setError('Could not copy the link. Select and copy the game URL below.') }
  }
  return <div className="admin-page">
    <header className="admin-heading"><span className="hero__eyebrow">SYSTEM / ADMINISTRATION</span><h1>Members</h1>
      <nav aria-label="Administration"><Link to="/admin">Models & access</Link> · <Link to="/admin/members" aria-current="page">Members</Link></nav>
      <p>Grant Player access by email. Players request a login code when they open the game.</p>
    </header>
    <section className="admin-models" aria-labelledby="invite-title">
      <h2 id="invite-title">Invite a Player</h2>
      <form className="member-invite" onSubmit={event => { event.preventDefault(); void change(async () => { await grantMember(email); setEmail('') }, 'Invitation saved. Access becomes ready after Cloudflare confirms the update.') }}>
        <label htmlFor="member-email">Email address</label>
        <input id="member-email" type="email" required maxLength={254} autoComplete="email" value={email} disabled={busy || !data} onChange={event => setEmail(event.target.value)} />
        <button type="submit" disabled={busy || !data}>Grant access</button>
      </form>
      {data && <p>Share this game link: <a href={data.gameUrl}>{data.gameUrl}</a> <button type="button" onClick={() => { void copyLink() }}>Copy game link</button></p>}
      <p>No invitation email is sent. Admin and Developer access remains managed separately.</p>
    </section>
    {(error || loadError) && <p role="alert">{error || loadError}</p>}
    {notice && <output className="admin-status">{notice}</output>}
    <section className="admin-models" aria-labelledby="members-title">
      <div className="admin-section-heading"><h2 id="members-title">Players</h2><button type="button" disabled={busy} onClick={() => setRefresh(value => value + 1)}>Refresh</button></div>
      {!data && !loadError && <output className="admin-status">Loading members…</output>}
      {data?.syncError && <div role="alert"><p>Cloudflare synchronization needs attention: {data.syncError}</p><button type="button" disabled={busy} onClick={() => { void change(retryMembers, 'Synchronization retry requested.') }}>Retry synchronization</button></div>}
      {data?.members.length === 0 && <p>No Players have been invited yet.</p>}
      <ul className="member-list">{data?.members.map(member => <li key={member.id} className="admin-model">
        <h3>{member.email}</h3><p>{accessLabel(member)} · {member.version === member.syncedVersion ? 'Cloudflare synchronized' : 'Cloudflare update pending'}</p>
        <button type="button" disabled={busy} aria-label={`${member.desiredAccess ? 'Remove access for' : 'Grant access to'} ${member.email}`} onClick={() => { void change(() => member.desiredAccess ? removeMember(member.id) : grantMember(member.email), member.desiredAccess ? 'Access removed from the game. Cloudflare removal is pending.' : 'Invitation saved. Waiting for Cloudflare.') }}>{member.desiredAccess ? 'Remove access' : 'Grant access'}</button>
      </li>)}</ul>
    </section>
  </div>
}
