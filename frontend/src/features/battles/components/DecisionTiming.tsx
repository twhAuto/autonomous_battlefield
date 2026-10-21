import { useEffect, useState } from 'react'
import type { DecisionHistory } from '../api/battles'

function duration(start?: string, end?: string | number) {
  if (!start || end === undefined) return 'Not recorded'
  const ms = (typeof end === 'number' ? end : Date.parse(end)) - Date.parse(start)
  return Number.isFinite(ms) && ms >= 0 ? `${(ms / 1000).toFixed(1)}s` : 'Not recorded'
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
  const label = pending ? 'Queued' : running ? 'Deciding' : decision.status === 'accepted' ? 'Plan accepted in' : 'Resolved in'
  return <div className="decision-timing">
    <p><strong>{label} · {duration(decision.createdAt, end)}</strong></p>
    <details><summary>Timing details</summary>
      <dl>
        <div><dt>Queue wait</dt><dd>{duration(decision.createdAt, decision.claimedAt || (pending && active ? now : undefined))}</dd></div>
        <div><dt>CLI process launch</dt><dd>{decision.cliProcessStartMs == null ? 'Not recorded' : `${decision.cliProcessStartMs}ms`}</dd></div>
        <div><dt>Claim to context served</dt><dd>{duration(decision.claimedAt, decision.contextServedAt)}</dd></div>
        <div><dt>Claim to resolution</dt><dd>{duration(decision.claimedAt, decision.resolvedAt)}</dd></div>
        <div><dt>Worker completion after resolution</dt><dd>{duration(decision.resolvedAt, decision.runnerCompletedAt)}</dd></div>
      </dl>
      <p>Process launch measures OS process creation, not CLI readiness. Context timing includes setup and tool requests. Internal model thinking time is not measured. Reclaimed jobs show the latest claim; total wait includes earlier attempts.</p>
    </details>
  </div>
}
