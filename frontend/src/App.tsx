import { useMemo } from 'react'
import { Link, NavLink, Outlet, Route, Routes } from 'react-router-dom'
import { CreateFactionPage } from './pages/CreateFactionPage'
import { CreateBattlePage } from './pages/CreateBattlePage'
import { BattlePage } from './pages/BattlePage'
import { FactionsPage } from './pages/FactionsPage'
import { HomePage } from './pages/HomePage'
import './styles.css'

function AppLayout() {
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
        </div>
      </header>

      <main id="main-content"><Outlet /></main>

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

export default function App() {
  return (
    <Routes>
      <Route element={<AppLayout />}>
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
