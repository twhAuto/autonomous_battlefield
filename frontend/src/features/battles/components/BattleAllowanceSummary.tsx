// Shows a Player's or Tester's battle allowance on battle setup and names the used-up
// limit that would reject a new battle.

import { Link } from 'react-router-dom'
import type { BattleAllowance } from '../api/battles'

export type AllowanceBlocker = 'daily' | 'unfinished' | 'service'

const reached = (count: number, limit?: number) => limit !== undefined && count >= limit

// allowanceBlocker names the first used-up allowance, in the order admission checks them.
export function allowanceBlocker(allowance?: BattleAllowance): AllowanceBlocker | undefined {
  if (!allowance?.limited) return undefined
  if (reached(allowance.startedToday, allowance.dailyLimit)) return 'daily'
  if (reached(allowance.unfinished, allowance.unfinishedLimit)) return 'unfinished'
  if (reached(allowance.serviceUnfinished, allowance.serviceUnfinishedLimit)) return 'service'
  return undefined
}

const battles = (count = 0) => `${count} ${count === 1 ? 'battle' : 'battles'}`

function BlockerMessage({ allowance, blocker, reset }: Readonly<{ allowance: BattleAllowance; blocker: AllowanceBlocker; reset: string }>) {
  if (blocker === 'daily') return <>You have started all {battles(allowance.dailyLimit)} allowed today. Your allowance resets {reset}.</>
  if (blocker === 'unfinished') {
    return <>You can have {battles(allowance.unfinishedLimit)} unfinished at a time, and paused battles count. Finish or stop one in <Link to="/battles">Battle history</Link>.</>
  }
  return <>The battlefield is at its limit of {battles(allowance.serviceUnfinishedLimit)} unfinished. Try again when one ends.</>
}

export function BattleAllowanceSummary({ allowance, onRefresh }: Readonly<{ allowance?: BattleAllowance; onRefresh: () => void }>) {
  if (!allowance?.limited) return null
  const reset = `${new Date(allowance.resetAt).toLocaleString()} (00:00 UTC)`
  const blocker = allowanceBlocker(allowance)
  return (
    <div className="battle-form__field" aria-live="polite">
      <p className="field-hint">Battles today: {allowance.startedToday} of {allowance.dailyLimit} started · resets {reset}</p>
      <p className="field-hint">Unfinished battles: {allowance.unfinished} of {allowance.unfinishedLimit} allowed at once</p>
      {blocker && <>
        <p className="field-error" role="alert"><BlockerMessage allowance={allowance} blocker={blocker} reset={reset} /></p>
        <button className="compact-action" type="button" onClick={onRefresh}>CHECK AGAIN</button>
      </>}
    </div>
  )
}
