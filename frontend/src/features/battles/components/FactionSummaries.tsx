import type { BattleParticipant, BattleRun, DecisionHistory } from '../api/battles'
import { usePermissions } from '../../auth/SessionContext'
import { latestDecision } from './AIDecisionPanel'
import { playerStatus } from './PlayerStatusPanel'

function activity(battle: BattleRun, participant: BattleParticipant, decisions: DecisionHistory[], message: string) {
  if (battle.status === 'completed') return 'Completed'
  if (battle.status === 'failed') return 'Battle failed'
  if (battle.status === 'stopped') return 'Stopped'
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

export function FactionSummaries({ battle, decisions, decisionMessage }: Readonly<{
  battle: BattleRun; decisions: DecisionHistory[]; decisionMessage: string
}>) {
  const { inspect } = usePermissions()
  return <section className="faction-summaries" aria-label="Faction summaries">
    {battle.participants.map((participant) => {
      const status = playerStatus(battle.state, participant.id)
      const applied = decisions.findLast((decision) => decision.factionId === participant.id
        && decision.targetTick <= battle.currentTick
        && (decision.status === 'accepted' || decision.status === 'fallback') && decision.resolvedPlan)
      const label = activity(battle, participant, decisions, decisionMessage)
      return <article key={participant.id} className={`faction-summary${inspect ? ' faction-summary--plans' : ''}`} aria-label={participant.name}>
        <h2 title={participant.name}>{participant.name}</h2>
        <p className={`faction-summary__activity${label === 'Deciding' || label === 'Queued' ? ' faction-summary__activity--active' : ''}`}>{label}</p>
        <dl className="faction-summary__metrics">
          <div><dt>Gold</dt><dd>{status.faction?.gold ?? '—'}</dd></div>
          <div><dt>Units</dt><dd>{status.faction && battle.state.units ? status.units.length : '—'}</dd></div>
        </dl>
        {inspect && <textarea className="faction-summary__plan" readOnly rows={6} aria-label={`${participant.name} plan and rationale`}
          value={`${applied?.status === 'fallback' ? 'Plan · fallback' : 'Plan'}\n${applied?.resolvedPlan?.name || (decisionMessage ? 'Plan unavailable' : 'No plan applied yet')}\n\nRationale\n${applied?.resolvedPlan?.rationale || 'No rationale recorded.'}`} />}
      </article>
    })}
  </section>
}
