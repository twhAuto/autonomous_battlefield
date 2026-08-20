import { Link } from 'react-router-dom'

export function HomePage() {
  return (
    <>
      <section className="hero">
        <div className="hero__eyebrow"><span>01</span> COMMAND INTERFACE</div>
        <h1>Choose your path.<br /><em>Shape the frontier.</em></h1>
        <p className="hero__copy">
          Establish a faction, inspect every active banner, or deploy two forces
          into a deterministic engagement.
        </p>
        <div className="hero__coordinates">47°12' N &nbsp; / &nbsp; SECTOR 09</div>
      </section>

      <section className="command-menu" aria-labelledby="menu-heading">
        <div className="section-label"><span>AVAILABLE OPERATIONS</span><span>02</span></div>
        <h2 id="menu-heading">Main menu</h2>
        <div className="menu-grid">
          <Link className="menu-card menu-card--primary" to="/factions/new">
            <span className="menu-card__number">01</span>
            <div>
              <span className="menu-card__eyebrow">NEW DEPLOYMENT</span>
              <h3>Create faction</h3>
              <p>Choose a designation, generate an identity, and raise a new banner.</p>
            </div>
            <span className="menu-card__arrow" aria-hidden="true">→</span>
          </Link>
          <Link className="menu-card" to="/factions">
            <span className="menu-card__number">02</span>
            <div>
              <span className="menu-card__eyebrow">ACTIVE REGISTRY</span>
              <h3>View all factions</h3>
              <p>Browse every faction currently registered on the frontier.</p>
            </div>
            <span className="menu-card__arrow" aria-hidden="true">→</span>
          </Link>
          <Link className="menu-card menu-card--battle" to="/battles/new">
            <span className="menu-card__number">03</span>
            <div>
              <span className="menu-card__eyebrow">COMBAT OPERATIONS</span>
              <h3>Stage a battle</h3>
              <p>Select two factions, queue an engagement, and follow its live event stream.</p>
            </div>
            <span className="menu-card__arrow" aria-hidden="true">→</span>
          </Link>
        </div>
      </section>
    </>
  )
}
