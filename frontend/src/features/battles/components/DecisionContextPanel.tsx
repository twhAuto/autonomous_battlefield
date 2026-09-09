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
    <summary>Saved decision context</summary>
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
  return <>
    <h3>What this AI knew at tick {data.observationTick}</h3>
    <p>Saved observation · plan for tick {data.targetTick}. Current player resources are shown in the header.</p>
    <dl>
      <div><dt>Gold then</dt><dd>{context.economy.gold}</dd></div>
      <div><dt>Projected income</dt><dd>{context.economy.projectedIncome}</dd></div>
      <div><dt>Own living units</dt><dd>{own}</dd></div>
      <div><dt>Visible enemy units</dt><dd>{units.length - own}</dd></div>
      <div><dt>Actions</dt><dd>{context.legal.actionKinds?.join(', ') || 'None recorded'}</dd></div>
      <div><dt>Available units</dt><dd>{context.legal.units?.map((unit) => `${typeLabel(unit.id)} (${unit.cost} gold)`).join(', ') || 'None recorded'}</dd></div>
    </dl>
    {data.initialContext && <ContextDetail label="Reconstructed agent context" value={data.initialContext} />}
    {data.availableUnitDetails && <ContextDetail label="Unit details available on demand" value={data.availableUnitDetails} />}
    <ContextDetail label="Visible battlefield and positions" value={context.view} />
    <ContextDetail label="Economy and available choices" value={{ economy: context.economy, legal: context.legal, ownedUpgradeIds: context.ownedUpgradeIds }} />
    <ContextDetail label="Prior active plan" value={context.activePlan} />
    <ContextDetail label="Saved faction instructions" value={{ background: context.background, doctrine: context.doctrine }} />
    <ContextDetail label="Saved memory" value={context.memory} />
    <ContextDetail label="Saved knowledge" value={context.knowledge} />
  </>
}

function ContextDetail({ label, value }: Readonly<{ label: string; value: unknown }>) {
  return <details><summary>{label}</summary><pre>{value == null ? 'Not recorded' : JSON.stringify(value, null, 2)}</pre></details>
}
