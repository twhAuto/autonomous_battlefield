// Model leaderboard: each controller's win rate as a dot, with its 95% Wilson interval
// as a whisker, on a shared 0–100% axis. One series, so the title names it and there is
// no legend; each row is a button that shows its record on hover, focus or tap. The
// models table repeats every value.

import { useState } from 'react'
import { controllerKey, controllerName, type ModelStats } from '../api/analytics'

const ticks = [0, 25, 50, 75, 100]

export function percent(value: number) {
  return `${Math.round(value * 100)}%`
}

export function WinRateChart({ models }: Readonly<{ models: ModelStats[] }>) {
  const [active, setActive] = useState<string | null>(null)
  return (
    <figure className="win-rate-chart">
      <figcaption>
        <strong>Win rate by controller</strong>
        <span>Dot: share of battles won. Line: 95% interval; wide lines mean few battles.</span>
      </figcaption>
      <div className="win-rate-chart__axis" aria-hidden="true">
        <span />
        <div className="win-rate-chart__ticks">{ticks.map(tick => <span key={tick} style={{ left: `${tick}%` }}>{tick}%</span>)}</div>
        <span />
      </div>
      <ul className="win-rate-chart__rows">
        {models.map(model => {
          const key = controllerKey(model.controller)
          const label = `${controllerName(model.controller)}: ${percent(model.winRate)} won of ${model.battles} battles, 95% interval ${percent(model.winRateLow)} to ${percent(model.winRateHigh)}`
          return (
            <li key={key}><button type="button" className="win-rate-chart__row" aria-label={label} onClick={() => setActive(key)}
              onPointerEnter={() => setActive(key)} onPointerLeave={() => setActive(null)} onFocus={() => setActive(key)} onBlur={() => setActive(null)}>
              <span className="win-rate-chart__label">{controllerName(model.controller)}</span>
              <span className="win-rate-chart__track">
                {ticks.map(tick => <span key={tick} className="win-rate-chart__grid" style={{ left: `${tick}%` }} />)}
                <span className="win-rate-chart__interval" style={{ left: `${model.winRateLow * 100}%`, width: `${(model.winRateHigh - model.winRateLow) * 100}%` }} />
                <span className="win-rate-chart__dot" style={{ left: `${model.winRate * 100}%` }} />
                {active === key && <span className="win-rate-chart__tooltip" role="tooltip" style={{ left: `${model.winRate * 100}%` }}>
                  <strong>{percent(model.winRate)}</strong> {model.wins}W {model.draws}D {model.losses}L
                  <small>{percent(model.winRateLow)}–{percent(model.winRateHigh)} · {model.battles} battles</small>
                </span>}
              </span>
              <span className="win-rate-chart__value">{percent(model.winRate)} <small>n={model.battles}</small></span>
            </button></li>
          )
        })}
      </ul>
    </figure>
  )
}
