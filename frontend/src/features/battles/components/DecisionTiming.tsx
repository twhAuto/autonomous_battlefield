import { useEffect, useState } from 'react'
import type { DecisionHistory } from '../api/battles'

function duration(start?: string, end?: string | number) {
  if (!start || end === undefined) return 'Not recorded'
  const ms = (typeof end === 'number' ? end : Date.parse(end)) - Date.parse(start)
  return Number.isFinite(ms) && ms >= 0 ? `${(ms / 1000).toFixed(1)}s` : 'Not recorded'
}

function timingLabel(status: DecisionHistory['status']) {
  switch (status) {
    case 'pending': return 'Queued'
    case 'running': return 'Deciding'
    case 'accepted': return 'Plan accepted in'
    default: return 'Resolved in'
  }
}

function deliveryLabel(mode: DecisionHistory['contextDelivery']) {
  if (mode === 'prefetch') return 'Host prefetch'
  if (mode === 'tool') return 'Model tool request'
  return 'Not recorded'
}

export function DecisionTiming({ decision, active }: Readonly<{ decision: DecisionHistory; active: boolean }>) {
  const [now, setNow] = useState(Date.now)
  const pending = decision.status === 'pending'
  const running = decision.status === 'running'
  useEffect(() => {
    if (!active || (!pending && !running)) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [active, pending, running])
  const end = decision.resolvedAt || ((pending || running) && active ? now : undefined)
  const label = timingLabel(decision.status)
  return <div className="decision-timing">
    <p><strong>{label} · {duration(decision.createdAt, end)}</strong></p>
    <details><summary>Timing details</summary>
      <dl>
        <div><dt>Context delivery</dt><dd>{deliveryLabel(decision.contextDelivery)}</dd></div>
        <div><dt>Queue wait</dt><dd>{duration(decision.createdAt, decision.claimedAt || (pending && active ? now : undefined))}</dd></div>
        <div><dt>CLI process launch</dt><dd>{decision.cliProcessStartMs == null ? 'Not recorded' : `${decision.cliProcessStartMs}ms`}</dd></div>
        <div><dt>Claim to context served</dt><dd>{duration(decision.claimedAt, decision.contextServedAt)}</dd></div>
        <div><dt>Claim to resolution</dt><dd>{duration(decision.claimedAt, decision.resolvedAt)}</dd></div>
        <div><dt>Worker completion after resolution</dt><dd>{duration(decision.resolvedAt, decision.runnerCompletedAt)}</dd></div>
      </dl>
      <p>Process launch measures OS process creation, not CLI readiness. With host prefetch, context is served before the CLI starts; otherwise context timing includes setup and model tool requests. Compare claim-to-resolution time across delivery modes. Internal model thinking time is not measured. Reclaimed jobs show the latest claim; total wait includes earlier attempts.</p>
    </details>
  </div>
}
