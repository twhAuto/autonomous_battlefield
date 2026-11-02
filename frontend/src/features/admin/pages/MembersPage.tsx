import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { getMembers, grantMember, removeMember, retryMembers, type Members, type Member, type MemberRole } from '../api/members'

const roleLabels: Record<MemberRole, string> = { player: 'Player', tester: 'Tester' }

function accessLabel(member: Member) {
  if (!member.desiredAccess) return 'Removed'
  return member.version === member.syncedVersion ? 'Ready to play' : 'Waiting for access'
}
function message(error: unknown) { return error instanceof Error ? error.message : 'The request failed. Please try again.' }

function RoleSelect({ id, label, value, disabled, onChange }: Readonly<{ id: string; label?: string; value: MemberRole; disabled: boolean; onChange: (role: MemberRole) => void }>) {
  return <select id={id} aria-label={label} value={value} disabled={disabled} onChange={event => onChange(event.target.value as MemberRole)}>
    {(Object.keys(roleLabels) as MemberRole[]).map(role => <option key={role} value={role}>{roleLabels[role]}</option>)}
  </select>
}

type Change = (action: () => Promise<void>, success: string) => void

// MemberRow changes an active member's role at once; removed members keep their role for a reinvite.
function MemberRow({ member, busy, change }: Readonly<{ member: Member; busy: boolean; change: Change }>) {
  const synchronized = member.version === member.syncedVersion
  return <li className="admin-model">
    <h3>{member.email}</h3>
    <p>{roleLabels[member.role]} · {accessLabel(member)} · {synchronized ? 'Cloudflare synchronized' : 'Cloudflare update pending'}</p>
    {member.desiredAccess && <RoleSelect id={`role-${member.id}`} label={`Role for ${member.email}`} value={member.role} disabled={busy}
      onChange={role => change(() => grantMember(member.email, role), `${member.email} is now a ${roleLabels[role]}. The change applies to their next request.`)} />}
    <button type="button" disabled={busy} aria-label={`${member.desiredAccess ? 'Remove access for' : 'Grant access to'} ${member.email}`}
      onClick={() => change(() => member.desiredAccess ? removeMember(member.id) : grantMember(member.email), member.desiredAccess ? 'Access removed from the game. Cloudflare removal is pending.' : 'Invitation saved. Waiting for Cloudflare.')}>
      {member.desiredAccess ? 'Remove access' : 'Grant access'}
    </button>
  </li>
}

export function MembersPage() {
  const [data, setData] = useState<Members | null>(null)
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<MemberRole>('player')
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

  async function run(action: () => Promise<void>, success: string) {
    setBusy(true); setError(''); setNotice('')
    try { await action(); setNotice(success); setRefresh(value => value + 1) }
    catch (error: unknown) { setError(message(error)) }
    finally { setBusy(false) }
  }
  const change: Change = (action, success) => { void run(action, success) }
  async function copyLink() {
    if (!data) return
    try { await navigator.clipboard.writeText(data.gameUrl); setNotice('Game link copied.') }
    catch { setError('Could not copy the link. Select and copy the game URL below.') }
  }
  return <div className="admin-page">
    <header className="admin-heading"><span className="hero__eyebrow">SYSTEM / ADMINISTRATION</span><h1>Members</h1>
      <nav aria-label="Administration"><Link to="/admin">Models & access</Link> · <Link to="/admin/members" aria-current="page">Members</Link></nav>
      <p>Grant Player or Tester access by email. Members request a login code when they open the game.</p>
    </header>
    <section className="admin-models" aria-labelledby="invite-title">
      <h2 id="invite-title">Invite a member</h2>
      <form className="member-invite" onSubmit={event => { event.preventDefault(); change(async () => { await grantMember(email, role); setEmail('') }, 'Invitation saved. Access becomes ready after Cloudflare confirms the update.') }}>
        <label htmlFor="member-email">Email address</label>
        <input id="member-email" type="email" required maxLength={254} autoComplete="email" value={email} disabled={busy || !data} onChange={event => setEmail(event.target.value)} />
        <label htmlFor="member-role">Role</label>
        <RoleSelect id="member-role" value={role} disabled={busy || !data} onChange={setRole} />
        <button type="submit" disabled={busy || !data}>Grant access</button>
      </form>
      {data && <p>Share this game link: <a href={data.gameUrl}>{data.gameUrl}</a> <button type="button" onClick={() => { void copyLink() }}>Copy game link</button></p>}
      <p>No invitation email is sent. Admins, and Testers set in server configuration, are managed separately.</p>
    </section>
    {(error || loadError) && <p role="alert">{error || loadError}</p>}
    {notice && <output className="admin-status">{notice}</output>}
    <section className="admin-models" aria-labelledby="members-title">
      <div className="admin-section-heading"><h2 id="members-title">Invited members</h2><button type="button" disabled={busy} onClick={() => setRefresh(value => value + 1)}>Refresh</button></div>
      {!data && !loadError && <output className="admin-status">Loading members…</output>}
      {data?.syncError && <div role="alert"><p>Cloudflare synchronization needs attention: {data.syncError}</p><button type="button" disabled={busy} onClick={() => change(retryMembers, 'Synchronization retry requested.')}>Retry synchronization</button></div>}
      {data?.members.length === 0 && <p>No members have been invited yet.</p>}
      <ul className="member-list">{data?.members.map(member => <MemberRow key={member.id} member={member} busy={busy} change={change} />)}</ul>
    </section>
  </div>
}
