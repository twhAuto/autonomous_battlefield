// Statistics across completed battles: the model leaderboard, head-to-head records,
// units, upgrades, factions, and map and side balance. One filter row scopes every
// section; filters live in the URL.

import { type ReactNode, useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { listLeagues, type LeagueSummary } from '../../leagues/api/leagues'
import { controllerKey, controllerName, getAnalytics, type AnalyticsReport, type Matchup, type ModelUnit } from '../api/analytics'
import { percent, WinRateChart } from '../components/WinRateChart'

const compact = new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 })

function ratio(value: number | null, digits = 2) {
  return value === null ? '—' : value.toFixed(digits)
}

export function AnalyticsPage() {
  const [params, setParams] = useSearchParams()
  const leagueId = params.get('leagueId') ?? ''
  const allRules = params.get('rules') === 'all'
  const excludeFallback = params.get('fallback') === 'exclude'
  const [report, setReport] = useState<AnalyticsReport | null>(null)
  const [leagues, setLeagues] = useState<LeagueSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    listLeagues(controller.signal).then(result => setLeagues(result.leagues)).catch(() => undefined)
    return () => controller.abort()
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    getAnalytics({ leagueId, allRules, excludeFallback }, controller.signal).then(result => {
      if (!controller.signal.aborted) setReport(result)
    }).catch((loadError: unknown) => {
      if (controller.signal.aborted) return
      // The previous report answers other filters; do not leave it under these ones.
      setReport(null)
      setError(loadError instanceof Error ? loadError.message : 'Statistics could not be loaded.')
    }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [leagueId, allRules, excludeFallback])

  function setFilter(name: string, value: string) {
    const next = new URLSearchParams(params)
    if (value) next.set(name, value)
    else next.delete(name)
    setParams(next)
  }

  return (
    <>
      <section className="page-header page-header--directory">
        <Link className="back-link" to="/">← MAIN MENU</Link>
        <div className="hero__eyebrow"><span>06</span> STATISTICS</div>
        <div className="directory-title"><div><h1>Statistics</h1><p>Which models win, which units and upgrades decide battles, and whether maps and sides are fair, across completed battles.</p></div>
          {leagueId && <Link className="compact-action" to={`/leagues/${encodeURIComponent(leagueId)}`}>← BACK TO LEAGUE</Link>}</div>
      </section>
      <form className="analytics-filters" aria-label="Statistics filters" onSubmit={event => event.preventDefault()}>
        <label>League <select value={leagueId} onChange={event => setFilter('leagueId', event.target.value)}>
          <option value="">All battles</option>
          {leagueId && !leagues.some(league => league.id === leagueId) && <option value={leagueId}>Selected league</option>}
          {leagues.map(league => <option key={league.id} value={league.id}>{league.name}</option>)}
        </select></label>
        <label><input type="checkbox" checked={allRules} onChange={event => setFilter('rules', event.target.checked ? 'all' : '')} /> Every rules version</label>
        <label><input type="checkbox" checked={excludeFallback} onChange={event => setFilter('fallback', event.target.checked ? 'exclude' : '')} /> Leave out battles with scripted fallbacks</label>
      </form>
      {error && <div className="directory-state directory-state--error" role="alert"><h2>Statistics unavailable</h2><p>{error}</p></div>}
      {!report && loading && !error && <output className="analytics-loading">Loading statistics…</output>}
      {report && <div className={loading ? 'analytics analytics--refreshing' : 'analytics'} aria-busy={loading}><ReportBody report={report} /></div>}
    </>
  )
}

function ReportBody({ report }: Readonly<{ report: AnalyticsReport }>) {
  if (report.battles === 0) {
    return <div className="directory-state"><h2>No completed battles match</h2><p>Finish some battles or a league, or widen the filters.</p></div>
  }
  return (
    <>
      <dl className="analytics-summary">
        <div><dt>Battles</dt><dd>{report.battles}</dd></div>
        <div><dt>Rules version</dt><dd>{report.rulesVersion ?? 'All'}</dd></div>
        <div><dt>With fallbacks</dt><dd>{report.battlesWithFallback}</dd></div>
        <div><dt>Left out for fallbacks</dt><dd>{report.excludedForFallback}</dd></div>
        <div><dt>Sides with unit data</dt><dd>{report.sidesWithUnitStats}</dd></div>
      </dl>
      {report.truncated && <p className="field-hint">Only the 5,000 most recent battles are included.</p>}
      <ModelsSection report={report} />
      <HeadToHead report={report} />
      <UnitsSection report={report} />
      <UpgradesSection report={report} />
      <FactionsSection report={report} />
      <MapsSection report={report} />
    </>
  )
}

function Section({ title, note, children }: Readonly<{ title: string; note?: string; children: ReactNode }>) {
  return (
    <section className="analytics-section" aria-label={title}>
      <div className="section-label"><span>{title.toUpperCase()}</span>{note && <span>{note}</span>}</div>
      <div className="analytics-scroll">{children}</div>
    </section>
  )
}

function ModelsSection({ report }: Readonly<{ report: AnalyticsReport }>) {
  return (
    <Section title="Models" note="BEST WIN RATE FIRST">
      <WinRateChart models={report.models} />
      <table className="analytics-table">
        <caption className="visually-hidden">Controller records and decision statistics</caption>
        <thead><tr><th scope="col">Controller</th><th scope="col">Battles</th><th scope="col">W–D–L</th><th scope="col">Win rate (95%)</th><th scope="col">Castle wins</th>
          <th scope="col">Ticks to win</th><th scope="col">Fallbacks</th><th scope="col">Invalid plans</th><th scope="col">Latency</th><th scope="col">Tokens in · cached · out</th></tr></thead>
        <tbody>{report.models.map(model => <tr key={controllerKey(model.controller)}>
          <th scope="row">{controllerName(model.controller)}</th><td>{model.battles}</td><td>{model.wins}–{model.draws}–{model.losses}</td>
          <td>{percent(model.winRate)} <small>{percent(model.winRateLow)}–{percent(model.winRateHigh)}</small></td><td>{model.castleWins}</td>
          <td>{ratio(model.meanTicksToWin, 0)}</td><td>{model.decisions ? percent(model.fallbackRate) : '—'}</td><td>{model.decisions ? percent(model.invalidPlanRate) : '—'}</td>
          <td>{model.meanDecisionLatencyMs === null ? '—' : `${(model.meanDecisionLatencyMs / 1000).toFixed(1)} s`}</td>
          <td>{compact.format(model.tokensPerBattle.input)} · {compact.format(model.tokensPerBattle.cachedInput)} · {compact.format(model.tokensPerBattle.output)}</td>
        </tr>)}</tbody>
      </table>
    </Section>
  )
}

function HeadToHead({ report }: Readonly<{ report: AnalyticsReport }>) {
  if (report.headToHead.length === 0) return null
  const controllers = report.models.map(model => model.controller)
  const cell = (row: string, column: string): Matchup | undefined => report.headToHead.find(m => controllerKey(m.controller) === row && controllerKey(m.opponent) === column)
  return (
    <Section title="Head to head" note="ROW'S RECORD AGAINST COLUMN, W–D–L">
      <table className="analytics-table analytics-matrix">
        <caption className="visually-hidden">Each controller's record against each other controller</caption>
        <thead><tr><th scope="col"><span className="visually-hidden">Controller</span></th>{controllers.map(c => <th key={controllerKey(c)} scope="col">{controllerName(c)}</th>)}</tr></thead>
        <tbody>{controllers.map(row => <tr key={controllerKey(row)}>
          <th scope="row">{controllerName(row)}</th>
          {controllers.map(column => {
            const match = cell(controllerKey(row), controllerKey(column))
            return <td key={controllerKey(column)}>{match ? `${match.wins}–${match.draws}–${match.losses}` : '—'}</td>
          })}
        </tr>)}</tbody>
      </table>
    </Section>
  )
}

function UnitsSection({ report }: Readonly<{ report: AnalyticsReport }>) {
  if (report.units.length === 0) return null
  return (
    <Section title="Units" note="SHARE OF RECRUITS IN WINNING VS LOSING ARMIES">
      <table className="analytics-table">
        <caption className="visually-hidden">Unit statistics</caption>
        <thead><tr><th scope="col">Unit</th><th scope="col">Fielded</th><th scope="col">Recruited</th><th scope="col">Lost</th><th scope="col">Kills</th>
          <th scope="col">Kills / loss</th><th scope="col">Damage / gold</th><th scope="col">In wins</th><th scope="col">In losses</th></tr></thead>
        <tbody>{report.units.map(unit => <tr key={unit.unitType}>
          <th scope="row" className="analytics-name">{unit.unitType.replaceAll('_', ' ')}</th><td>{unit.fielded}</td><td>{unit.recruited}</td><td>{unit.lost}</td><td>{unit.kills}</td>
          <td>{ratio(unit.killsPerLoss)}</td><td>{ratio(unit.damagePerGold)}</td><td>{percent(unit.shareInWins)}</td><td>{percent(unit.shareInLosses)}</td>
        </tr>)}</tbody>
      </table>
      <ArmyMix report={report} />
    </Section>
  )
}

function ArmyMix({ report }: Readonly<{ report: AnalyticsReport }>) {
  const controllers = report.models.map(model => model.controller).filter(c => report.unitsByModel.some(u => controllerKey(u.controller) === controllerKey(c)))
  const share = (unitType: string, key: string): ModelUnit | undefined => report.unitsByModel.find(u => u.unitType === unitType && controllerKey(u.controller) === key)
  return (
    <table className="analytics-table analytics-matrix">
      <caption>Army mix: each controller's recruits by unit type</caption>
      <thead><tr><th scope="col">Unit</th>{controllers.map(c => <th key={controllerKey(c)} scope="col">{controllerName(c)}</th>)}</tr></thead>
      <tbody>{report.units.map(unit => <tr key={unit.unitType}>
        <th scope="row" className="analytics-name">{unit.unitType.replaceAll('_', ' ')}</th>
        {controllers.map(c => {
          const entry = share(unit.unitType, controllerKey(c))
          return <td key={controllerKey(c)}>{entry ? percent(entry.armyShare) : '—'}</td>
        })}
      </tr>)}</tbody>
    </table>
  )
}

function UpgradesSection({ report }: Readonly<{ report: AnalyticsReport }>) {
  if (report.upgrades.length === 0) return null
  return (
    <Section title="Upgrades" note="WIN RATE OF SIDES THAT BOUGHT IT VS DID NOT">
      <table className="analytics-table">
        <caption className="visually-hidden">Upgrade statistics</caption>
        <thead><tr><th scope="col">Upgrade</th><th scope="col">Purchases</th><th scope="col">Purchase rate</th><th scope="col">Mean tick</th><th scope="col">Won with</th><th scope="col">Won without</th></tr></thead>
        <tbody>{report.upgrades.map(upgrade => <tr key={upgrade.upgradeId}>
          <th scope="row" className="analytics-name">{upgrade.upgradeId.replaceAll('_', ' ')}</th><td>{upgrade.purchases}</td><td>{percent(upgrade.purchaseRate)}</td><td>{Math.round(upgrade.meanPurchaseTick)}</td>
          <td>{upgrade.winRateWhenPurchased === null ? '—' : percent(upgrade.winRateWhenPurchased)}</td><td>{upgrade.winRateWhenNot === null ? '—' : percent(upgrade.winRateWhenNot)}</td>
        </tr>)}</tbody>
      </table>
    </Section>
  )
}

function FactionsSection({ report }: Readonly<{ report: AnalyticsReport }>) {
  return (
    <Section title="Factions" note="RESULTS INCLUDE THE CONTROLLER THAT PLAYED THEM">
      <table className="analytics-table">
        <caption className="visually-hidden">Faction records</caption>
        <thead><tr><th scope="col">Faction</th><th scope="col">Battles</th><th scope="col">W–D–L</th><th scope="col">Win rate (95%)</th><th scope="col">Castle wins</th></tr></thead>
        <tbody>{report.factions.map(faction => <tr key={faction.factionId}>
          <th scope="row">{faction.factionName}</th><td>{faction.battles}</td><td>{faction.wins}–{faction.draws}–{faction.losses}</td>
          <td>{percent(faction.winRate)} <small>{percent(faction.winRateLow)}–{percent(faction.winRateHigh)}</small></td><td>{faction.castleWins}</td>
        </tr>)}</tbody>
      </table>
    </Section>
  )
}

function MapsSection({ report }: Readonly<{ report: AnalyticsReport }>) {
  return (
    <Section title="Maps and sides">
      <table className="analytics-table">
        <caption className="visually-hidden">Map balance</caption>
        <thead><tr><th scope="col">Map</th><th scope="col">Battles</th><th scope="col">Draw rate</th><th scope="col">Mean ticks</th><th scope="col">Side 1 wins</th><th scope="col">Side 2 wins</th></tr></thead>
        <tbody>{report.maps.map(map => <tr key={map.scenarioId}>
          <th scope="row">{map.scenarioId}</th><td>{map.battles}</td><td>{percent(map.drawRate)}</td><td>{Math.round(map.meanTicks)}</td>
          <td>{map.sides[0].wins} <small>{percent(map.sides[0].winRate)}</small></td><td>{map.sides[1].wins} <small>{percent(map.sides[1].winRate)}</small></td>
        </tr>)}</tbody>
      </table>
    </Section>
  )
}
