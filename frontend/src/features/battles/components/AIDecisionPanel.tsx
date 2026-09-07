// Formats faction controller and decision history for the battle page, including plan
// purchases, outcomes, and fallback information.

import { BattleRun, DecisionHistory } from '../api/battles'

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
  if (decision.status === 'pending') return 'analyzing'
  if (decision.status === 'running') return 'MCP connected'
  if (decision.status === 'accepted') {
    const planName = decision.resolvedPlan?.name ? `: ${decision.resolvedPlan.name}` : ''
    return `plan accepted${planName}`
  }
  if (decision.status === 'fallback') return decision.fallbackSource === 'previous_plan' ? 'previous-plan fallback' : 'scripted fallback'
  const errorCategory = decision.errorCategory ? `: ${decision.errorCategory}` : ''
  return `decision failed${errorCategory}`
}

export function AIDecisionPanel({ decisions, participantNames, message, hasModelController }: Readonly<{ decisions: DecisionHistory[]; participantNames: Map<string, string>; message: string; hasModelController: boolean }>) {
  const modelDecisions = decisions.filter((decision) => decision.controllerType === 'model' || Boolean(decision.profileId))
  return (
    <section className="ai-decision-panel" aria-label="AI decisions" aria-live="polite">
      <div className="ai-decision-panel__header">
        <div><span>AI DECISION STREAM</span><h2>Strategy plans</h2></div>
        <strong>{modelDecisions.length}</strong>
      </div>
      {message && <div className="ai-decision-panel__state ai-decision-panel__state--error" role="alert">{message}</div>}
      {!message && modelDecisions.length === 0 && <div className="ai-decision-panel__state">{hasModelController ? 'Waiting for the opening decision.' : 'This battle uses scripted controllers, so no model decisions will be created.'}</div>}
      {modelDecisions.length > 0 && (
        <ol className="ai-decision-list">
          {[...modelDecisions].reverse().map((decision) => (
            <li key={decision.requestId}>
              <div className="ai-decision-list__heading">
                <div><strong>{participantNames.get(decision.factionId) ?? decision.factionId}</strong><span>{decision.targetTick === 0 ? 'OPENING' : `TICK ${decision.targetTick}`}</span></div>
                <span className={`decision-state decision-state--${decision.status}`}>{decisionLabel(decision)}</span>
              </div>
              <dl>
                <DecisionDetail label="Plan" value={decision.resolvedPlan?.name ?? (decision.status === 'running' || decision.status === 'pending' ? 'Analyzing…' : 'No plan submitted')} />
                <DecisionDetail label="Rationale" value={decision.resolvedPlan?.rationale ?? '—'} />
                <DecisionDetail label="Tactics" value={formatWeights(decision.resolvedPlan?.actionWeights)} />
                <DecisionDetail label="Reserve" value={decision.resolvedPlan ? `${decision.resolvedPlan.reserveGold ?? 0} gold` : '—'} />
                <DecisionDetail label="Spend order" value={formatList(decision.resolvedPlan?.spendOrder)} />
                <DecisionDetail label="Recruit" value={formatRecruitment(decision.resolvedPlan)} />
                <DecisionDetail label="Upgrades" value={formatList(decision.resolvedPlan?.upgradeOrders)} />
                <DecisionDetail label="Build sites" value={formatConstruction(decision.resolvedPlan)} />
                <DecisionDetail label="Model" value={decision.modelIdentifier || decision.profileId || '—'} />
              </dl>
            </li>
          ))}
        </ol>
      )}
    </section>
  )
}

function DecisionDetail({ label, value }: Readonly<{ label: string; value: string }>) {
  return <div><dt>{label}</dt><dd>{value}</dd></div>
}

function formatWeights(weights?: Record<string, number>) {
  if (!weights || Object.keys(weights).length === 0) return '—'
  return Object.entries(weights).map(([name, weight]) => `${name} ${weight}`).join(' · ')
}

function formatList(values?: string[]) {
  return values?.length ? values.join(' → ') : '—'
}

function formatCoordinates(values?: { x: number; y: number }[]) {
  return values?.length ? values.map(({ x, y }) => `(${x},${y})`).join(' → ') : '—'
}

function formatRecruitment(plan?: DecisionHistory['resolvedPlan']) {
  if (plan?.recruitmentOrders != null) {
    return plan.recruitmentOrders.length ? plan.recruitmentOrders.map(({ unitType, count, sourceBuildingId }) => `${count} × ${unitType}${sourceBuildingId ? ` from ${sourceBuildingId}` : ''}`).join(' → ') : 'No purchases'
  }
  return formatList(plan?.recruitmentPriorities)
}

function formatConstruction(plan?: DecisionHistory['resolvedPlan']) {
  if (plan?.constructionOrders != null) {
    return plan.constructionOrders.length ? plan.constructionOrders.map(({ buildingType, position }) => `${buildingType} (${position.x},${position.y})`).join(' → ') : 'No construction'
  }
  return formatCoordinates(plan?.constructionPriorities)
}
