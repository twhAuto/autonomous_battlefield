import type { CSSProperties } from 'react'
import type { BattleParticipant, BattleRun, DecisionHistory } from '../api/battles'
import { usePermissions } from '../../auth/SessionContext'
import { factionColors, unknownFactionColor } from '../presentation/factions'
import { latestDecision } from './AIDecisionPanel'
import { ProductionChips } from './BuildingProduction'
import { playerStatus } from './PlayerStatusPanel'
import { UpgradeChips } from './UpgradeIcon'

// Settled battles need no activity: the page title already says how the battle ended.
function activity(battle: BattleRun, participant: BattleParticipant, decisions: DecisionHistory[], message: string) {
  if (battle.status === 'completed' || battle.status === 'failed' || battle.status === 'stopped') return ''
  if (battle.paused) return 'Paused'
  if (participant.controller?.type !== 'model') return 'Scripted'
  if (message) return 'Status unavailable'
  const decision = latestDecision(decisions, participant.id)
  if (!decision) return 'Awaiting decision'
  const labels: Record<DecisionHistory['status'], string> = {
    pending: 'Queued', running: 'Deciding', accepted: 'Plan ready',
    fallback: 'Fallback', failed: 'Decision failed',
  }
  return labels[decision.status]
}

// Player responses carry only the controller type, so the model stays hidden from them.
function modelLabel(participant: BattleParticipant) {
  const controller = participant.controller
  if (controller?.type !== 'model' || !controller.modelIdentifier) return ''
  return controller.reasoningEffort ? `${controller.modelIdentifier} · ${controller.reasoningEffort}` : controller.modelIdentifier
}

// Units count living units plus paid units in training, the total the unit cap limits;
// buildings count living structures other than the castle, which has its own metric.
function forces(battle: BattleRun, status: ReturnType<typeof playerStatus>) {
  if (!status.faction || !battle.state.units) return { units: '—', unitsTitle: undefined, buildings: '—' }
  const training = status.buildings.reduce((total, building) => total + (building.trainingQueue?.length ?? 0), 0)
  const count = status.units.length + training
  const cap = battle.maximumUnits ?? 0
  const breakdown = `${status.units.length} on the field, ${training} in training`
  return {
    units: cap ? `${count} / ${cap}` : String(count),
    unitsTitle: cap ? `${breakdown}; cap ${cap}` : breakdown,
    buildings: String(status.buildings.filter((building) => building.type !== 'castle').length),
  }
}

// One compact strip per faction: identity, economy, forces, production and upgrade chips and,
// for viewers who may inspect decisions, the applied plan with its rationale behind a disclosure.
export function FactionSummaries({ battle, decisions, decisionMessage }: Readonly<{
  battle: BattleRun; decisions: DecisionHistory[]; decisionMessage: string
}>) {
  const { inspect } = usePermissions()
  const colors = factionColors(battle.participants.map((participant) => participant.id))
  return <section className="faction-summaries" aria-label="Faction summaries">
    {battle.participants.map((participant) => {
      const status = playerStatus(battle.state, participant.id)
      const applied = decisions.findLast((decision) => decision.factionId === participant.id
        && decision.targetTick <= battle.currentTick
        && (decision.status === 'accepted' || decision.status === 'fallback') && decision.resolvedPlan)
      const label = activity(battle, participant, decisions, decisionMessage)
      const force = forces(battle, status)
      const model = modelLabel(participant)
      const planName = applied?.resolvedPlan?.name || (decisionMessage ? 'Plan unavailable' : 'No plan applied yet')
      const planLabel = applied?.status === 'fallback' ? 'Plan · fallback' : 'Plan'
      const activityClass = `faction-summary__activity${label === 'Deciding' || label === 'Queued' ? ' faction-summary__activity--active' : ''}`
      return <article key={participant.id} className="faction-summary" aria-label={participant.name} style={{ '--faction-color': colors.get(participant.id) ?? unknownFactionColor } as CSSProperties}>
        <div className="faction-summary__header">
          <h2 title={model ? `${participant.name} (${model})` : participant.name}>
            <span className="faction-summary__name">{participant.name}</span>
            {model && <span className="faction-summary__model">{model}</span>}
          </h2>
          <dl className="faction-summary__metrics">
            <div><dt>Gold</dt><dd>{status.faction?.gold ?? '—'}</dd></div>
            <div title={force.unitsTitle}><dt>Units</dt><dd>{force.units}</dd></div>
            <div><dt>Buildings</dt><dd>{force.buildings}</dd></div>
            <div><dt>Castle</dt><dd>{status.castleHealth}</dd></div>
          </dl>
        </div>
        <div className="faction-summary__operations">
          <div className="faction-summary__assets">
            {battle.state && <ProductionChips state={battle.state} factionId={participant.id} factionName={participant.name} group={`faction-${participant.id}`} />}
            <UpgradeChips upgradeIds={status.faction?.ownedUpgradeIds} factionName={participant.name} group={`faction-${participant.id}`} />
          </div>
          {!inspect && label && <p className={activityClass}>{label}</p>}
          {inspect && <details className="faction-summary__plan" name={`faction-${participant.id}`}>
            <summary title={`${planLabel}: ${planName}`}>{label && <><span className={activityClass}>{label}</span> <span aria-hidden="true">·</span></>} {applied?.status === 'fallback' && <span>fallback</span>} <strong>{planName}</strong></summary>
            <textarea readOnly rows={8} aria-label={`${participant.name} plan and rationale`}
              value={`${planLabel}\n${planName}\n\nRationale\n${applied?.resolvedPlan?.rationale || 'No rationale recorded.'}`} />
          </details>}
        </div>
      </article>
    })}
  </section>
}
