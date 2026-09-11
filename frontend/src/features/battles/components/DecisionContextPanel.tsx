import { typeLabel } from '../presentation/entities'
import { useEffect, useState } from 'react'
import { BattleApiError, getDecisionContext, type SavedDecisionContext } from '../api/battles'

export type DecisionContextCache = Map<string, SavedDecisionContext>

export function DecisionContextPanel({ battleId, requestId, attempt, active, cache }: Readonly<{
  battleId: string; requestId: string; attempt: number; active: boolean; cache: DecisionContextCache
}>) {
  const [open, setOpen] = useState(false)
  const [data, setData] = useState<SavedDecisionContext>()
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const key = `${battleId}:${attempt}:${requestId}`
  useEffect(() => {
    if (!active || !open) return
    const stored = cache.get(key)
    if (stored) { setData(stored); return }
    const controller = new AbortController()
    setData(undefined)
    setError('')
    getDecisionContext(battleId, requestId, attempt, controller.signal).then((result) => {
      if (controller.signal.aborted) return
      cache.set(key, result)
      setData(result)
    }).catch((reason: unknown) => {
      if (!controller.signal.aborted) setError(reason instanceof BattleApiError ? reason.message : 'Decision context could not be loaded.')
    })
    return () => controller.abort()
  }, [active, open, key, cache, battleId, requestId, attempt, retry])

  return <details className="saved-context" open={open} onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary>Decision context snapshot</summary>
    {open && <div>
      {error && <p role="alert">{error} <button className="text-button" type="button" onClick={() => setRetry((value) => value + 1)}>Retry context</button></p>}
      {!error && !data && <output>Loading saved context…</output>}
      {data && (data.context ? <Observation data={data} context={data.context} /> : <p>No observation was saved for this decision.</p>)}
    </div>}
  </details>
}

function Observation({ data, context }: Readonly<{ data: SavedDecisionContext; context: NonNullable<SavedDecisionContext['context']> }>) {
  const units = (context.view.state?.units ?? []).filter((unit) => unit.health > 0)
  const own = units.filter((unit) => unit.factionId === data.factionId).length
  return <section className="saved-context__snapshot" aria-label="Decision context details">
    <header className="saved-context__header">
      <h3>Observation snapshot</h3>
      <p>Observed tick {data.observationTick} <span aria-hidden="true">·</span> Decision tick {data.targetTick}</p>
    </header>
    <dl className="saved-context__metrics">
      <div><dt>Gold</dt><dd>{context.economy.gold}</dd></div>
      <div><dt>Projected income</dt><dd>{context.economy.projectedIncome}</dd></div>
      <div><dt>Friendly units</dt><dd>{own}</dd></div>
      <div><dt>Visible enemies</dt><dd>{units.length - own}</dd></div>
      <div><dt>Permitted actions</dt><dd>{context.legal.actionKinds?.join(', ') || 'None recorded'}</dd></div>
      <div><dt>Recruitment options</dt><dd>{context.legal.units?.map((unit) => `${typeLabel(unit.id)} (${unit.cost} gold)`).join(', ') || 'None recorded'}</dd></div>
    </dl>
    {data.initialContext && <ContextDetail label="Agent context (reconstructed)" value={data.initialContext} />}
    {data.availableUnitDetails && <ContextDetail label="On-demand unit reference" value={data.availableUnitDetails} />}
    <ContextDetail label="Battlefield state" value={context.view} />
    <ContextDetail label="Economy and legal actions" value={{ economy: context.economy, legal: context.legal, ownedUpgradeIds: context.ownedUpgradeIds }} />
    <ContextDetail label="Active plan" value={context.activePlan} />
    <ContextDetail label="Faction instructions" value={{ background: context.background, doctrine: context.doctrine }} />
    <ContextDetail label="Memory" value={context.memory} />
    <ContextDetail label="Knowledge" value={context.knowledge} />
  </section>
}

function ContextDetail({ label, value }: Readonly<{ label: string; value: unknown }>) {
  return <details><summary>{label}</summary><pre>{value == null ? 'Not recorded' : JSON.stringify(value, null, 2)}</pre></details>
}
