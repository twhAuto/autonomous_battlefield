import { type SubmitEvent, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  createFaction,
  FactionApiError,
  type FactionRuleset,
  listFactionRulesets,
} from '../api/factions'
import { Emblem } from '../components/Emblem'

function newFactionId() { return crypto.randomUUID() }

export function CreateFactionPage() {
  const [name, setName] = useState('')
  const [factionId, setFactionId] = useState(newFactionId)
  const [rulesets, setRulesets] = useState<FactionRuleset[]>([])
  const [rulesetId, setRulesetId] = useState('')
  const [counts, setCounts] = useState<Record<string, number>>({})
  const [upgradeIds, setUpgradeIds] = useState<string[]>([])
  const [priority, setPriority] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [status, setStatus] = useState<'idle' | 'submitting' | 'success' | 'error'>('idle')
  const [message, setMessage] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    listFactionRulesets(controller.signal).then((loaded) => {
      setRulesets(loaded)
      if (loaded[0]) selectRuleset(loaded[0])
      setLoading(false)
    }).catch((error) => {
      if (controller.signal.aborted) return
      setLoading(false)
      setStatus('error')
      setMessage(error instanceof FactionApiError ? error.message : 'Faction design rules could not be loaded.')
    })
    return () => controller.abort()
  }, [])

  const ruleset = rulesets.find((value) => value.id === rulesetId)
  const spent = useMemo(() => {
    if (!ruleset) return 0
    const unitCost = ruleset.units.reduce((sum, unit) => sum + unit.cost * (counts[unit.id] ?? 0), 0)
    const upgradeCost = ruleset.upgrades.reduce((sum, upgrade) => sum + (upgradeIds.includes(upgrade.id) ? upgrade.cost : 0), 0)
    return unitCost + upgradeCost
  }, [counts, ruleset, upgradeIds])
  const remaining = (ruleset?.designBudget ?? 0) - spent
  const totalUnits = Object.values(counts).reduce((sum, count) => sum + count, 0)
  const trimmedName = name.trim()
  const legalBlueprint = Boolean(ruleset) && totalUnits > 0 && totalUnits <= 6 && remaining >= 0
  const canSubmit = trimmedName.length > 0 && legalBlueprint && status !== 'submitting'

  function selectRuleset(selected: FactionRuleset) {
    const defaults = Object.fromEntries(selected.units.map((unit) => [unit.id, 1]))
    setRulesetId(selected.id)
    setCounts(defaults)
    setPriority(selected.units.map((unit) => unit.id))
    setUpgradeIds(selected.upgrades.some((upgrade) => upgrade.id === 'arsenal') ? ['arsenal'] : [])
  }

  function changeCount(unitId: string, delta: number) {
    setCounts((current) => {
      const next = Math.max(0, Math.min(6, (current[unitId] ?? 0) + delta))
      setPriority((order) => next > 0
        ? (order.includes(unitId) ? order : [...order, unitId])
        : order.filter((id) => id !== unitId))
      return { ...current, [unitId]: next }
    })
    setStatus('idle')
  }

  function movePriority(index: number, direction: -1 | 1) {
    const target = index + direction
    if (target < 0 || target >= priority.length) return
    setPriority((current) => {
      const next = [...current]
      ;[next[index], next[target]] = [next[target], next[index]]
      return next
    })
  }

  async function handleSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit || !ruleset) {
      setStatus('error')
      setMessage('Choose at least one unit and stay within the design budget.')
      return
    }
    setStatus('submitting')
    setMessage('')
    try {
      await createFaction({
        id: factionId,
        name: trimmedName,
        rulesetId: ruleset.id,
        blueprint: {
          units: ruleset.units.filter((unit) => (counts[unit.id] ?? 0) > 0).map((unit) => ({ type: unit.id, count: counts[unit.id] })),
          upgradeIds,
          recruitmentPriority: priority,
        },
      })
      setStatus('success')
      setMessage(`${trimmedName} has entered the battlefield.`)
      setName('')
      setFactionId(newFactionId())
    } catch (error) {
      setStatus('error')
      setMessage(error instanceof FactionApiError ? error.message : 'An unexpected error occurred. Please try again.')
    }
  }

  return (
    <>
      <section className="page-header">
        <Link className="back-link" to="/">← MAIN MENU</Link>
        <div className="hero__eyebrow"><span>01</span> FACTION REGISTRY</div>
        <h1>Create a faction</h1>
        <p>Spend design points on a reusable army blueprint. Every battle starts fresh.</p>
      </section>

      <section className="creation-layout creation-layout--blueprint" aria-labelledby="creation-heading">
        <div className="form-panel">
          <div className="section-label"><span>POINT-BUY BLUEPRINT</span><span>{remaining} PTS LEFT</span></div>
          <h2 id="creation-heading">Faction identity</h2>
          {loading ? <p>Loading faction rules…</p> : (
            <form onSubmit={handleSubmit} noValidate>
              <label htmlFor="faction-name">Faction name</label>
              <div className="name-field">
                <input id="faction-name" value={name} onChange={(event) => { setName(event.target.value); setStatus('idle') }} placeholder="e.g. The Verdant Accord" autoComplete="off" />
                <span>{name.length}</span>
              </div>

              <label htmlFor="ruleset">Ruleset</label>
              <select id="ruleset" value={rulesetId} onChange={(event) => { const selected = rulesets.find((item) => item.id === event.target.value); if (selected) selectRuleset(selected) }}>
                {rulesets.map((item) => <option key={item.id} value={item.id}>{item.id} v{item.version}</option>)}
              </select>

              {ruleset && <>
                <fieldset className="blueprint-fieldset">
                  <legend>Starting units</legend>
                  {ruleset.units.map((unit) => (
                    <div className="point-buy-row" key={unit.id}>
                      <div><strong>{unit.id}</strong><small>{unit.cost} pts · HP {unit.health} · ATK {unit.attack} · DEF {unit.defense}</small></div>
                      <div className="quantity-control">
                        <button type="button" aria-label={`Remove ${unit.id}`} onClick={() => changeCount(unit.id, -1)}>−</button>
                        <output aria-label={`${unit.id} quantity`}>{counts[unit.id] ?? 0}</output>
                        <button type="button" aria-label={`Add ${unit.id}`} onClick={() => changeCount(unit.id, 1)}>+</button>
                      </div>
                    </div>
                  ))}
                </fieldset>

                <fieldset className="blueprint-fieldset">
                  <legend>Upgrades</legend>
                  {ruleset.upgrades.map((upgrade) => (
                    <label className="upgrade-toggle" key={upgrade.id}>
                      <input type="checkbox" checked={upgradeIds.includes(upgrade.id)} onChange={() => setUpgradeIds((current) => current.includes(upgrade.id) ? current.filter((id) => id !== upgrade.id) : [...current, upgrade.id])} />
                      <span><strong>{upgrade.id}</strong><small>{upgrade.cost} points</small></span>
                    </label>
                  ))}
                </fieldset>

                <fieldset className="blueprint-fieldset">
                  <legend>Recruitment priority</legend>
                  <ol className="priority-list">
                    {priority.map((unitId, index) => <li key={unitId}><span>{index + 1}. {unitId}</span><span><button type="button" aria-label={`Move ${unitId} up`} onClick={() => movePriority(index, -1)}>↑</button><button type="button" aria-label={`Move ${unitId} down`} onClick={() => movePriority(index, 1)}>↓</button></span></li>)}
                  </ol>
                </fieldset>
              </>}

              <div className={`budget-meter ${remaining < 0 ? 'budget-meter--error' : ''}`}><strong>{spent} / {ruleset?.designBudget ?? 0} points</strong><span>{remaining >= 0 ? `${remaining} unspent` : `${Math.abs(remaining)} over budget`}</span></div>
              <div className="identity-block"><div><span className="identity-block__label">SYSTEM IDENTIFIER</span><code title={factionId}>{factionId}</code></div><button type="button" className="icon-button" onClick={() => setFactionId(newFactionId())} aria-label="Generate a new faction identifier">↻</button></div>
              <button className="submit-button" type="submit" disabled={!canSubmit}><span>{status === 'submitting' ? 'DEPLOYING…' : 'CREATE FACTION'}</span><span>→</span></button>
              <div className="form-message" aria-live="polite">{message && <p className={`form-message--${status}`}>{message}</p>}{status === 'success' && <Link className="text-link" to="/factions">View faction registry →</Link>}</div>
            </form>
          )}
        </div>

        <aside className="preview-panel" aria-label="Faction blueprint preview">
          <div className="preview-panel__grid" /><div className="preview-panel__heading"><span>BLUEPRINT PREVIEW</span><span>{spent} PTS</span></div>
          <div className="preview-panel__content"><Emblem seed={name.trim() || 'Your faction'} /><span className="preview-panel__caption">DESIGNATION</span><h3>{name.trim() || 'Your faction'}</h3><div className="preview-panel__rule" /><p>{Object.entries(counts).filter(([, count]) => count > 0).map(([id, count]) => `${count}× ${id}`).join(' · ') || 'No units selected'}</p><p>{upgradeIds.length ? upgradeIds.join(' · ') : 'No upgrades'}</p></div>
        </aside>
      </section>
    </>
  )
}
