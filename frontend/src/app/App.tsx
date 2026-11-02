// Application shell and route table. Checks the session, handles login/logout and API
// authentication expiry, and wraps feature pages in shared navigation.

import { useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { useMobile } from '../shared/hooks/useMobile'
import { Link, NavLink, Outlet, Route, Routes, useLocation } from 'react-router-dom'
import { CreateFactionPage } from '../features/factions/pages/CreateFactionPage'
import { CreateBattlePage } from '../features/battles/pages/CreateBattlePage'
import { BattleHistoryPage } from '../features/battles/pages/BattleHistoryPage'
import { BattlePage } from '../features/battles/pages/BattlePage'
import { FactionsPage } from '../features/factions/pages/FactionsPage'
import { HomePage } from '../pages/HomePage'
import { LoginPage } from '../features/auth/pages/LoginPage'
import { MembersPage } from '../features/admin/pages/MembersPage'
import { AdminSettingsPage } from '../features/admin/pages/AdminSettingsPage'
import { LeaguesPage } from '../features/leagues/pages/LeaguesPage'
import { CreateLeaguePage } from '../features/leagues/pages/CreateLeaguePage'
import { LeaguePage } from '../features/leagues/pages/LeaguePage'
import { AnalyticsPage } from '../features/analytics/pages/AnalyticsPage'
import { authenticationClient, type AuthenticationClient, type AuthSession } from '../features/auth/api/auth'
import { RolePreviewContext, previewRoleNames, type PreviewRole } from '../features/auth/RolePreviewContext'
import { authenticationRequiredEvent, setRequestPreviewRole } from '../shared/api/request'
import { SessionContext } from '../features/auth/SessionContext'
import '../styles.css'

function AppLayout({ actorID, admin, inspect, play, logoutError, onLogout }: Readonly<{ actorID: string; admin: boolean; inspect: boolean; play: boolean; logoutError: string; onLogout(): void }>) {
  const location = useLocation()
  const preview = useContext(RolePreviewContext)
  const mobile = useMobile()
  const [menuOpen, setMenuOpen] = useState(false)
  const menuButton = useRef<HTMLButtonElement>(null)
  useEffect(() => { setMenuOpen(false) }, [location.pathname, mobile])
  useEffect(() => {
    if (!menuOpen) return
    function closeOnEscape(event: globalThis.KeyboardEvent) {
      if (event.key !== 'Escape') return
      setMenuOpen(false)
      menuButton.current?.focus()
    }
    document.addEventListener('keydown', closeOnEscape)
    return () => document.removeEventListener('keydown', closeOnEscape)
  }, [menuOpen])
  function closeMobileMenu() {
    if (!mobile) return
    setMenuOpen(false)
    menuButton.current?.focus()
  }
  const isBattleDetail = /^\/battles\/[^/]+$/.test(location.pathname)
  const currentDate = useMemo(
    () => new Intl.DateTimeFormat('en', {
      month: 'short',
      day: '2-digit',
      year: 'numeric',
    }).format(new Date()),
    [],
  )

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">Skip to main content</a>
      <header className="topbar">
        <Link className="brand" to="/" aria-label="OrdinalFrontier home">
          <span className="brand__glyph" aria-hidden="true">O</span>
          <span className="brand__name">OrdinalFrontier</span>
        </Link>
        {mobile && <button ref={menuButton} type="button" className="menu-toggle" aria-expanded={menuOpen} aria-controls="primary-navigation" onClick={() => setMenuOpen(value => !value)}>Menu</button>}
        <nav id="primary-navigation" className="primary-nav" aria-label="Primary navigation" hidden={mobile && !menuOpen}>
          {play && <NavLink to="/battles/new" onClick={closeMobileMenu}>Stage battle</NavLink>}
          <NavLink to="/factions" onClick={closeMobileMenu}>Factions</NavLink>
          <NavLink to="/battles" end onClick={closeMobileMenu}>Battle history</NavLink>
          {inspect && <NavLink to="/leagues" onClick={closeMobileMenu}>Leagues</NavLink>}
          {inspect && <NavLink to="/analytics" onClick={closeMobileMenu}>Statistics</NavLink>}
          {admin && <NavLink to="/admin" onClick={closeMobileMenu}>Admin</NavLink>}
          {mobile && <div className="mobile-account"><span>Signed in as {actorID}</span><button type="button" onClick={onLogout} aria-label={`Log out ${actorID}`}>Log out</button></div>}
        </nav>
        <div className="topbar__status">
          <span className="status-dot" />
          <span>FRONTIER ONLINE</span>
          <span className="topbar__divider" />
          {currentDate.toUpperCase()}
          <button className="logout-button" type="button" onClick={onLogout} aria-label={`Log out ${actorID}`}>LOG OUT</button>
        </div>
      </header>

      {preview.role && <output className="role-preview-banner"><span>Viewing as {previewRoleNames[preview.role]} · Read-only preview using your own account. Your administrator permissions are unchanged.</span><button type="button" onClick={() => preview.setRole(null)}>Exit preview</button></output>}
      {!play && !preview.role && <output className="role-preview-banner"><span>Guest access · View only. You can watch battles and browse factions, but not change anything.</span></output>}
      {logoutError && <p className="logout-error" role="alert">{logoutError}</p>}
      <main id="main-content" tabIndex={-1} className={isBattleDetail ? 'main-content--battle' : undefined}><Outlet /></main>

      <footer>
        <span>OrdinalFrontier / COMMAND INTERFACE</span>
      </footer>
    </div>
  )
}

function NotFoundPage() {
  return (
    <section className="state-page">
      <span className="state-page__code">404 / UNKNOWN SECTOR</span>
      <h1>This route is outside the mapped frontier.</h1>
      <Link className="text-link" to="/">Return to main menu →</Link>
    </section>
  )
}

interface AppProps {
  readonly authentication?: AuthenticationClient
  readonly initialSession?: AuthSession
}

export default function App({ authentication = authenticationClient, initialSession }: Readonly<AppProps>) {
  const [session, setSession] = useState<AuthSession | null>(initialSession ?? null)
  const [previewRole, setPreviewRole] = useState<PreviewRole | null>(null)
  const changePreviewRole = useCallback((role: PreviewRole | null) => { setRequestPreviewRole(role); setPreviewRole(role) }, [])
  useEffect(() => () => setRequestPreviewRole(null), [])
  const [sessionError, setSessionError] = useState('')
  const [logoutError, setLogoutError] = useState('')
  const [sessionAttempt, setSessionAttempt] = useState(0)

  useEffect(() => {
    if (initialSession) return
    let active = true
    authentication.session().then((nextSession) => {
      if (active) setSession(nextSession)
    }).catch((error: unknown) => {
      if (active) setSessionError(error instanceof Error ? error.message : 'Authentication status could not be checked.')
    })
    return () => { active = false }
  }, [authentication, initialSession, sessionAttempt])

  useEffect(() => {
    const requireAuthentication = () => { changePreviewRole(null); setSession((previous) => ({ enabled: true, authenticated: false, mode: previous?.mode })) }
    window.addEventListener(authenticationRequiredEvent, requireAuthentication)
    return () => window.removeEventListener(authenticationRequiredEvent, requireAuthentication)
  }, [changePreviewRole])

  async function logout() {
    changePreviewRole(null)
    setLogoutError('')
    try {
      await authentication.logout()
      setSession(previous => ({ enabled: true, authenticated: false, mode: previous?.mode }))
    } catch {
      setLogoutError('Logout could not be confirmed. Try Log out again.')
    }
  }

  const canAdmin = session?.actor?.role === 'admin' || (!session?.actor?.role && session?.mode !== 'cloudflare')

  const effectiveSession = useMemo(() => session && canAdmin && previewRole ? { ...session, actor: { ...session?.actor, id: session?.actor?.id ?? 'local-development', role: previewRole }, capabilities: { inspect: previewRole === 'tester', admin: false } } : session, [session, canAdmin, previewRole])
  const previewContext = useMemo(() => ({ role: previewRole, setRole: changePreviewRole }), [previewRole, changePreviewRole])
  const adminNow = canAdmin && !previewRole
  const effectiveRole = effectiveSession?.actor?.role ?? (session?.mode === 'cloudflare' ? 'player' : 'admin')
  const inspect = effectiveRole === 'admin' || effectiveRole === 'tester'
  const play = effectiveRole !== 'guest'

  if (sessionError) {
    return (
      <main className="login-page">
        <section className="login-card" aria-labelledby="session-error-title">
          <span className="login-card__eyebrow">CONNECTION FAILURE</span>
          <h1 id="session-error-title">Command post unavailable.</h1>
          <p role="alert">{sessionError}</p>
          <button className="submit-button" type="button" onClick={() => { setSessionError(''); setSessionAttempt((attempt) => attempt + 1) }}>
            <span>RETRY CONNECTION</span><span>→</span>
          </button>
        </section>
      </main>
    )
  }
  if (!session) {
    return <main className="login-page"><div className="loader" aria-label="Checking authentication" /></main>
  }
  if (!session.authenticated) {
    if (session.mode === 'cloudflare') return <main className="login-page"><section className="login-card"><h1>Sign in to Battlefield</h1><p>Your session has expired or access is unavailable.</p><button type="button" onClick={() => window.location.assign('/')}>Sign in</button></section></main>
    return <LoginPage authentication={authentication} onAuthenticated={setSession} />
  }

  return (
    <RolePreviewContext.Provider value={previewContext}><SessionContext.Provider value={effectiveSession ?? session}><Routes key={previewRole ?? 'admin'}>
      <Route element={<AppLayout actorID={session.actor?.id ?? 'local-development'} admin={adminNow} inspect={inspect} play={play} logoutError={logoutError} onLogout={logout} />}>
        <Route index element={<HomePage />} />
        {play && <><Route path="factions/new" element={<CreateFactionPage />} />
          <Route path="factions/:id/edit" element={<CreateFactionPage />} /></>}
        <Route path="battles/new" element={play ? <CreateBattlePage /> : <NotFoundPage />} />
        <Route path="factions" element={<FactionsPage />} />
        <Route path="battles" element={<BattleHistoryPage />} />
        <Route path="battles/:id" element={<BattlePage />} />
        {inspect && <><Route path="leagues" element={<LeaguesPage canCreate={adminNow} />} />
          <Route path="leagues/new" element={adminNow ? <CreateLeaguePage /> : <NotFoundPage />} />
          <Route path="leagues/:id" element={<LeaguePage canManage={adminNow} />} />
          <Route path="analytics" element={<AnalyticsPage />} /></>}
        {canAdmin && !previewRole && <><Route path="admin" element={<AdminSettingsPage />} />{session.capabilities?.members && <Route path="admin/members" element={<MembersPage />} />}</>}
        <Route path="*" element={<NotFoundPage />} />
      </Route>
    </Routes></SessionContext.Provider></RolePreviewContext.Provider>
  )
}
