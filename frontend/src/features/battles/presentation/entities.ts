// Presentation-only names. Generated IDs remain the durable identity, including in
// historical events where an entity no longer exists in the current snapshot.
import type { BattleState } from '../api/battles'

export interface EntityIdentity { id: string; type: string; factionId: string }
export interface EntityNaming {
  entities?: ReadonlyMap<string, EntityIdentity>
  factionNames?: ReadonlyMap<string, string>
  localFactionId?: string
}

export function typeLabel(type: string) {
  return type.replaceAll('_', ' ').replaceAll('-', ' ').replace(/\b\w/g, (letter) => letter.toUpperCase())
}

export function entityIndex(state?: Pick<BattleState, 'units' | 'buildings'>) {
  return new Map<string, EntityIdentity>([...(state?.units ?? []), ...(state?.buildings ?? [])].map((entity) => [entity.id, entity]))
}

export function entityName(id: string | undefined, naming: EntityNaming = {}) {
  if (!id) return 'Unknown entity'
  const entity = naming.entities?.get(id)
  const parts = id.split(':')
  const last = parts.pop()!
  const sequence = /^\d+$/.test(last) ? last.replace(/^0+(?=\d)/, '') : undefined
  const type = sequence === undefined ? last : parts.pop()
  const generated = parts.length > 0 && Boolean(type) && (sequence !== undefined || type === 'castle')
  const factionId = entity?.factionId ?? (generated ? parts.join(':') : undefined)
  const label = typeLabel(entity?.type ?? (generated ? type! : 'entity'))
  const generatedIdentity = sequence === undefined ? label : `${label} #${sequence}`
  const identity = generated ? generatedIdentity : `${label} · ${id.slice(0, 8)}`
  if (!factionId || factionId === naming.localFactionId) return identity
  const faction = naming.factionNames?.get(factionId) ?? `Faction · ${factionId.slice(0, 8)}`
  return `${faction} · ${identity}`
}

const entityFields = new Set(['unitId', 'buildingId', 'castleId', 'attackerId', 'targetId', 'healerId', 'sourceId', 'sourceBuildingId', 'attackTargetId', 'healTargetId'])
const typeFields = new Set(['unitType', 'buildingType', 'targetType'])

// Only structured reference/type fields are rewritten; arbitrary strings and AI
// prose must not be interpreted as identifiers.
export function eventStringLabel(key: string, value: string, naming: EntityNaming) {
  if (entityFields.has(key)) return entityName(value, naming)
  if (typeFields.has(key)) return typeLabel(value)
  if (key === 'factionId' || key.endsWith('FactionId')) return naming.factionNames?.get(value) ?? value
  return value
}
