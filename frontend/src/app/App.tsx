import { useEffect, useMemo, useState } from 'react'
import { Link, NavLink, Outlet, Route, Routes, useLocation } from 'react-router-dom'
import { CreateFactionPage } from '../features/factions/pages/CreateFactionPage'
import { CreateBattlePage } from '../features/battles/pages/CreateBattlePage'
import { BattlePage } from '../features/battles/pages/BattlePage'
import { FactionsPage } from '../features/factions/pages/FactionsPage'
import { HomePage } from '../pages/HomePage'
import { LoginPage } from '../features/auth/pages/LoginPage'
import { authenticationClient, type AuthenticationClient, type AuthSession } from '../features/auth/api/auth'
import { authenticationRequiredEvent } from '../shared/api/request'
import '../styles.css'

function AppLayout({ actorID, onLogout }: Readonly<{ actorID: string; onLogout(): void }>) {
  const location = useLocation()
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
      <header className="topbar">
        <Link className="brand" to="/" aria-label="Autonomous Battlefield home">
          <span className="brand__glyph">A</span>
          <span>AUTONOMOUS<br />BATTLEFIELD</span>
        </Link>
        <nav className="primary-nav" aria-label="Primary navigation">
          <NavLink to="/battles/new">Stage battle</NavLink>
          <NavLink to="/factions/new">Create faction</NavLink>
          <NavLink to="/factions">All factions</NavLink>
        </nav>
        <div className="topbar__status">
          <span className="status-dot" />
          <span>FRONTIER ONLINE</span>
          <span className="topbar__divider" />
          {currentDate.toUpperCase()}
          <button className="logout-button" type="button" onClick={onLogout} aria-label={`Log out ${actorID}`}>LOG OUT</button>
        </div>
      </header>

      <main id="main-content" className={isBattleDetail ? 'main-content--battle' : undefined}><Outlet /></main>

      <footer>
        <span>AUTONOMOUS BATTLEFIELD / COMMAND INTERFACE</span>
        <span>BUILD 0.3.0</span>
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
  const [sessionError, setSessionError] = useState('')
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
    const requireAuthentication = () => setSession({ enabled: true, authenticated: false })
    window.addEventListener(authenticationRequiredEvent, requireAuthentication)
    return () => window.removeEventListener(authenticationRequiredEvent, requireAuthentication)
  }, [])

  async function logout() {
    try {
      await authentication.logout()
    } finally {
      setSession({ enabled: true, authenticated: false })
    }
  }

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
    return <LoginPage authentication={authentication} onAuthenticated={setSession} />
  }

  return (
    <Routes>
      <Route element={<AppLayout actorID={session.actor?.id ?? 'local-development'} onLogout={logout} />}>
        <Route index element={<HomePage />} />
        <Route path="factions/new" element={<CreateFactionPage />} />
        <Route path="factions" element={<FactionsPage />} />
        <Route path="battles/new" element={<CreateBattlePage />} />
        <Route path="battles/:id" element={<BattlePage />} />
        <Route path="*" element={<NotFoundPage />} />
      </Route>
    </Routes>
  )
}
