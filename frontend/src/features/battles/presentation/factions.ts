// Faction colors and model labels shared by the map, faction strips and results, so a
// strip matches its units and every view names a faction's model the same way.
import type { BattleParticipant } from '../api/battles'

const palette = ['#69d2ff', '#ff6b55']
export const unknownFactionColor = '#f2c94c'

// Colors follow the participant roster sorted by ID, independent of entity order.
// Code-unit order, not localeCompare: it must match the backend's byte-order ID sort.
export function factionColors(roster: Iterable<string>) {
  const sorted = [...roster].sort((a, b) => (a < b ? -1 : Number(a > b)))
  return new Map(sorted.map((factionId, index) => [factionId, palette[Math.min(index, palette.length - 1)]]))
}

// Scripted controllers have no model, so they get no label.
export function modelLabel(participant: BattleParticipant | undefined) {
  const controller = participant?.controller
  if (controller?.type !== 'model' || !controller.modelIdentifier) return ''
  return controller.reasoningEffort ? `${controller.modelIdentifier} · ${controller.reasoningEffort}` : controller.modelIdentifier
}
