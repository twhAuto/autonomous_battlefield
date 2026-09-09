// Formats faction controller and decision history for the battle page, including plan
// purchases, outcomes, and fallback information.

import { Fragment, type ReactNode, useState } from 'react'
import { entityName, typeLabel, type EntityNaming } from '../presentation/entities'
import { BattleRun, DecisionHistory } from '../api/battles'
import { DecisionContextPanel, type DecisionContextCache } from './DecisionContextPanel'

export function latestDecision(decisions: DecisionHistory[], factionId: string) {
  return decisions.findLast((decision) => decision.factionId === factionId)
}

export function controllerLabel(controller?: BattleRun['participants'][number]['controller']) {
  if (!controller) return 'Scripted'
  if (controller.type === 'scripted') return 'Scripted'
  return controller.modelIdentifier || controller.profileId || controller.provider || 'Model'
}

export function decisionLabel(decision?: DecisionHistory) {
  if (!decision) return 'awaiting window'
  if (decision.status === 'pending') return 'queued for analysis'
  if (decision.status === 'running') return 'decision in progress'
  if (decision.status === 'accepted') {
    const planName = decision.resolvedPlan?.name ? `: ${decision.resolvedPlan.name}` : ''
    return `plan accepted${planName}`
  }
  if (decision.status === 'fallback') return decision.fallbackSource === 'previous_plan' ? 'previous-plan fallback' : 'scripted fallback'
  const errorCategory = decision.errorCategory ? `: ${decision.errorCategory}` : ''
  return `decision failed${errorCategory}`
}

export function AIDecisionPanel({ decisions, participantNames, message, hasModelController, selectedFaction, onSelectFaction, attempt, battleId, active = true, contextCache, embedded = false, entities }: Readonly<{
  entities?: NonNullable<EntityNaming['entities']>
  decisions: DecisionHistory[]; participantNames: Map<string, string>; message: string; hasModelController: boolean
  selectedFaction?: string; onSelectFaction?: (factionId: string) => void; attempt?: number
  battleId?: string; active?: boolean; contextCache?: DecisionContextCache; embedded?: boolean
}>) {
  const [localCache] = useState<DecisionContextCache>(() => new Map())
  const [localFaction, setLocalFaction] = useState('all')
  const faction = selectedFaction ?? localFaction
  const modelDecisions = decisions.filter((decision) => decision.controllerType === 'model' || Boolean(decision.profileId))
  const visibleDecisions = faction === 'all' ? modelDecisions : decisions.filter((decision) => decision.factionId === faction)
  const allPlayersEmpty = hasModelController ? 'Waiting for the opening decision.' : 'This battle uses scripted controllers, so no model decisions will be created.'
  return (
    <section className="ai-decision-panel" aria-label="AI decisions" aria-live="polite">
      <div className="ai-decision-panel__header">
        <div><span>AI DECISION STREAM{attempt ? ` · ATTEMPT ${attempt}` : ''}</span><h2>Decision inspector</h2></div>
        <strong>{visibleDecisions.length}</strong>
      </div>
      {!embedded && <label className="panel-filter" htmlFor="decision-player">Player<select id="decision-player" value={faction} onChange={(event) => (onSelectFaction ?? setLocalFaction)(event.target.value)}><option value="all">All AI players</option>{[...participantNames].map(([id, name]) => <option value={id} key={id}>{name}</option>)}</select></label>}
      <p className="decision-inspector-note">Submitted rationale explains the plan. Orders are intentions; the event stream records execution.</p>
      {message && <div className="ai-decision-panel__state ai-decision-panel__state--error" role="alert">{message}</div>}
      {!message && visibleDecisions.length === 0 && <div className="ai-decision-panel__state">{faction !== 'all' ? 'No decisions recorded for this player in the current attempt.' : allPlayersEmpty}</div>}
      {visibleDecisions.length > 0 && (
        <ol className="ai-decision-list">
          {[...visibleDecisions].reverse().map((decision, index) => (
            <DecisionCard key={decision.requestId} decision={decision} entities={entities} participantNames={participantNames} initiallyOpen={index === 0} battleId={battleId} attempt={attempt} active={active} cache={contextCache ?? localCache} />
          ))}
        </ol>
      )}
    </section>
  )
}

function DecisionCard({ decision, participantNames, initiallyOpen, battleId, attempt, active, cache, entities }: Readonly<{
  entities?: NonNullable<EntityNaming['entities']>
  decision: DecisionHistory; participantNames: Map<string, string>; initiallyOpen: boolean
  battleId?: string; attempt?: number; active: boolean; cache: DecisionContextCache
}>) {
  const [open, setOpen] = useState(initiallyOpen)
  return (
    <li>
      <details open={open} onToggle={(event) => setOpen(event.currentTarget.open)}>
        <summary className="ai-decision-list__heading">
          <div><strong>{participantNames.get(decision.factionId) ?? decision.factionId}</strong><span>{decision.targetTick === 0 ? 'OPENING' : `TICK ${decision.targetTick}`}</span></div>
          <span className={`decision-state decision-state--${decision.status}`}>{decisionLabel(decision)}</span>
        </summary>
        <dl>
          <DecisionDetail label="Plan" value={decision.resolvedPlan?.name ?? (decision.status === 'running' || decision.status === 'pending' ? 'Analyzing…' : 'No plan submitted')} />
          <DecisionDetail label="Rationale" value={decision.resolvedPlan?.rationale || 'No rationale recorded.'} />
          <DecisionDetail label="Tactics" value={formatWeights(decision.resolvedPlan?.actionWeights)} />
          <DecisionDetail label="Targets" value={formatWeights(decision.resolvedPlan?.targetBonuses, typeLabel)} />
          <DecisionDetail label="Reserve" value={decision.resolvedPlan ? `${decision.resolvedPlan.reserveGold ?? 0} gold` : '—'} />
          <DecisionDetail label="Spend order" value={formatList(decision.resolvedPlan?.spendOrder)} />
          <DecisionDetail label="Recruit" value={formatRecruitment(decision.resolvedPlan, { entities, factionNames: participantNames, localFactionId: decision.factionId })} />
          <DecisionDetail label="Upgrades" value={formatList(decision.resolvedPlan?.upgradeOrders)} />
          <DecisionDetail label="Build sites" value={formatConstruction(decision.resolvedPlan)} />
          <DecisionDetail label="Model" value={decision.modelIdentifier || decision.profileId || '—'} />
          <DecisionDetail label="Controller" value={decision.controllerType} />
          <DecisionDetail label="Provider" value={decision.provider || '—'} />
          <DecisionDetail label="Runtime" value={decision.runtime || '—'} />
          <DecisionDetail label="Requested" value={decision.createdAt || '—'} />
          <DecisionDetail label="Resolved" value={decision.resolvedAt || 'Not resolved'} />
          <DecisionDetail label="Fallback" value={decision.fallbackSource || 'None'} />
          <DecisionDetail label="Error" value={decision.errorCategory || 'None'} />
          <DecisionDetail label="Tokens in / out" value={`${decision.usage.inputTokens ?? (decision.usage.tokensReported ? 0 : '—')} / ${decision.usage.outputTokens ?? (decision.usage.tokensReported ? 0 : '—')}`} />
          <DecisionDetail label="Usage scope" value={usageScopeLabel(decision.usage.scope)} />
          <DecisionDetail label="Cached input tokens" value={String(decision.usage.cachedInputTokens ?? '—')} />
          <DecisionDetail label="Reasoning output tokens" value={String(decision.usage.reasoningOutputTokens ?? '—')} />
          <DecisionDetail label="Model calls" value={String(decision.usage.modelCalls ?? '—')} />
          <DecisionDetail label="CLI invocations" value={String(decision.usage.cliInvocations ?? '—')} />
          {!decision.usage.scope && <DecisionDetail label="Requests (legacy)" value={String(decision.usage.requests ?? '—')} />}
          {decision.traceUrl && <DecisionDetail label="Trace" value={<a href={decision.traceUrl} target="_blank" rel="noopener noreferrer">View in Langfuse</a>} />}
        </dl>
        {battleId && attempt !== undefined && attempt > 0 && <DecisionContextPanel battleId={battleId} requestId={decision.requestId} attempt={attempt} active={active && open} cache={cache} />}
      </details>
    </li>
  )
}

function DecisionDetail({ label, value }: Readonly<{ label: string; value: ReactNode }>) {
  return <div><dt>{label}</dt><dd>{value}</dd></div>
}

function formatWeights(weights?: Record<string, number>, formatName = (name: string) => name) {
  if (!weights || Object.keys(weights).length === 0) return '—'
  return Object.entries(weights).map(([name, weight]) => `${formatName(name)} ${weight}`).join(' · ')
}

function formatList(values?: string[]) {
  return values?.length ? values.join(' → ') : '—'
}

function formatCoordinates(values?: { x: number; y: number }[]) {
  return values?.length ? values.map(({ x, y }) => `(${x},${y})`).join(' → ') : '—'
}

function formatRecruitment(plan: DecisionHistory['resolvedPlan'], naming: EntityNaming) {
  if (plan?.recruitmentOrders != null) {
    return plan.recruitmentOrders.length ? plan.recruitmentOrders.map(({ unitType, count, sourceBuildingId }, index) => <Fragment key={`${unitType}:${sourceBuildingId}:${index}`}>{index > 0 && ' → '}{count} × {typeLabel(unitType)}{sourceBuildingId && <> from <span title={sourceBuildingId}>{entityName(sourceBuildingId, naming)}</span></>}</Fragment>) : 'No purchases'
  }
  return formatList(plan?.recruitmentPriorities?.map(typeLabel))
}

function formatConstruction(plan?: DecisionHistory['resolvedPlan']) {
  if (plan?.constructionOrders != null) {
    return plan.constructionOrders.length ? plan.constructionOrders.map(({ buildingType, position }) => `${typeLabel(buildingType)} (${position.x},${position.y})`).join(' → ') : 'No construction'
  }
  return formatCoordinates(plan?.constructionPriorities)
}

function usageScopeLabel(scope: string | undefined) {
  if (scope === 'cli_session') return 'CLI session total'
  if (scope === 'api_calls') return 'Sum of API calls'
  return 'Legacy / unspecified'
}
