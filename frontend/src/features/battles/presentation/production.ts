// Derives production-window timing from the battle's immutable decision interval.

/** Engine limit on units one faction may request per production window. */
export const MAX_RECRUITS_PER_WINDOW = 6

export interface ProductionWindow {
  nextTick: number
  ticksAway: number
}

/**
 * The engine applies plans and spends gold on the transition into each tick that
 * is a multiple of the interval, so the next window is the first such tick after
 * the current one. Returns null when the interval is unknown.
 */
export function nextProductionWindow(currentTick: number, intervalTicks: number | undefined): ProductionWindow | null {
  if (!intervalTicks || intervalTicks < 1) return null
  const nextTick = (Math.floor(currentTick / intervalTicks) + 1) * intervalTicks
  return { nextTick, ticksAway: nextTick - currentTick }
}
