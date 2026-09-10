// Compact entry points for battles, faction management, and history.
import { Link } from 'react-router-dom'

export function HomePage() {
  return <section className="command-menu command-menu--compact" aria-labelledby="menu-heading">
    <h1 id="menu-heading">Main menu</h1>
    <div className="menu-grid">
      <Link className="menu-card menu-card--primary menu-card--battle" to="/battles/new">
        <span className="menu-card__number">01</span>
        <div><h2>Stage battle</h2><p>Choose your factions and start a battle.</p></div>
        <span className="menu-card__arrow" aria-hidden="true">→</span>
      </Link>
      <Link className="menu-card" to="/factions">
        <span className="menu-card__number">02</span>
        <div><h2>Factions</h2><p>Create a faction or manage your existing factions.</p></div>
        <span className="menu-card__arrow" aria-hidden="true">→</span>
      </Link>
      <Link className="menu-card" to="/battles">
        <span className="menu-card__number">03</span>
        <div><h2>Battle history</h2><p>Review results and return to active battles.</p></div>
        <span className="menu-card__arrow" aria-hidden="true">→</span>
      </Link>
    </div>
  </section>
}
