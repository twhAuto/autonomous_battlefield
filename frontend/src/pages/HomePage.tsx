// Entry points for play, faction management, and role-appropriate reports.
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { usePermissions } from '../features/auth/SessionContext'

function MenuCard({ to, number, title, description, children }: Readonly<{
  to: string; number: string; title: string; description: string; children: ReactNode
}>) {
  return <Link className="menu-card" to={to} aria-labelledby={`menu-title-${number}`} aria-describedby={`menu-description-${number}`}>
    <div className="menu-card__top" aria-hidden="true">
      <span className="menu-card__number">{number}</span>
      <svg className="menu-card__icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">{children}</svg>
    </div>
    <div className="menu-card__body">
      <h2 id={`menu-title-${number}`}>{title}</h2>
      <p id={`menu-description-${number}`}>{description}</p>
    </div>
    <span className="menu-card__arrow" aria-hidden="true">↗</span>
  </Link>
}

export function HomePage() {
  const { inspect } = usePermissions()
  return <section className="command-menu" aria-labelledby="menu-heading">
    <header className="command-menu__header">
      <h1 id="menu-heading">Main menu</h1>
      <p>Choose your next move.</p>
    </header>
    <div className={`menu-grid${inspect ? '' : ' menu-grid--play'}`}>
      <Link className="menu-card menu-card--battle" to="/battles/new" aria-labelledby="menu-title-01" aria-describedby="menu-description-01">
        <div className="menu-card__top" aria-hidden="true"><span className="menu-card__number">01</span><span className="menu-card__eyebrow">BATTLE SETUP</span></div>
        <svg className="menu-card__map" viewBox="0 0 320 180" fill="none" aria-hidden="true">
          <path className="menu-card__contour" d="m80 16 64 36v72l-64 36-64-36V52Zm0 22 44 25v50l-44 25-44-25V63Zm160-22 64 36v72l-64 36-64-36V52Zm0 22 44 25v50l-44 25-44-25V63Z" />
          <path className="menu-card__route" d="M80 88h48l32-28 32 56h48V88" />
          <path className="menu-card__castle" d="M66 102V76h8v8h12v-8h8v26Zm160 0V76h8v8h12v-8h8v26Z" />
          <circle cx="160" cy="60" r="4" fill="currentColor" />
        </svg>
        <div className="menu-card__body">
          <h2 id="menu-title-01">Stage battle</h2>
          <p id="menu-description-01">Choose your factions, set the battlefield and put your strategy to the test.</p>
        </div>
        <span className="menu-card__action" aria-hidden="true">Prepare a battle <span>→</span></span>
      </Link>
      <MenuCard to="/factions" number="02" title="Factions" description="Create a faction and shape its doctrine.">
        <path d="m12 3 8 3v6c0 4-5 7-8 9-3-2-8-5-8-9V6Zm0 5v8m-4-4h8" />
      </MenuCard>
      <MenuCard to="/battles" number="03" title="Battle history" description="Return to active battles or review the results.">
        <path d="M4 8a8 8 0 1 1 0 8M4 3v5h5m3-1v5l3 2" />
      </MenuCard>
      {inspect && <>
        <MenuCard to="/leagues" number="04" title="Leagues" description="Follow tournaments, standings and champions.">
          <path d="M8 3h8v7a4 4 0 0 1-8 0Zm8 2h4v3a4 4 0 0 1-4 4M8 5H4v3a4 4 0 0 0 4 4m4 2v6m-4 1h8" />
        </MenuCard>
        <MenuCard to="/analytics" number="05" title="Statistics" description="Compare models, factions and battlefield performance.">
          <path d="M4 3v18h17M8 16v-4m5 4V8m5 8V4" />
        </MenuCard>
      </>}
    </div>
  </section>
}
