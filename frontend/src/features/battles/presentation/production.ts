// Presents the engine-computed production schedule. All purchases for a window happen
// at once; nothing is queued between windows.

import type { BattleRun } from '../api/battles'

/** Engine limit on units one faction may request per production window. */
export const MAX_RECRUITS_PER_WINDOW = 6

/**
 * Describes the next spending point for an active battle, or null when there is
 * nothing to show (settled battle, or a response without the field).
 */
export function productionSummary(battle: BattleRun): string | null {
  if (battle.status !== 'queued' && battle.status !== 'running') return null
  if (battle.production === undefined) return null
  if (battle.production === null) return 'No production windows remain before the tick limit'
  const limit = `up to ${MAX_RECRUITS_PER_WINDOW} units per faction`
  const paused = battle.paused ? ', paused' : ''
  if (battle.production.opening) return `Opening purchase pending (before tick 1${paused}) · ${limit}`
  const ticksAway = battle.production.nextTick - (battle.state?.tick ?? battle.currentTick)
  return `Next production window: tick ${battle.production.nextTick} (in ${ticksAway} ${ticksAway === 1 ? 'tick' : 'ticks'}${paused}) · ${limit}`
}
