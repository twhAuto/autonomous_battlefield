import { describe, expect, it } from 'vitest'
import { entityIndex, entityName, eventStringLabel, typeLabel } from './entities'

const faction = '12c47cbf-5ce9-42c7-a9f9-dd31ca58b2a4'
const factionNames = new Map([[faction, 'Iron Legion'], ['enemy', 'Northwind']])
const id = `${faction}:ranger:012`

describe('entity presentation', () => {
  it('uses durable sequence numbers and distinguishes factions in shared views', () => {
    expect(entityName(id, { factionNames })).toBe('Iron Legion · Ranger #12')
    expect(entityName(id, { factionNames, localFactionId: faction })).toBe('Ranger #12')
    expect(entityName('enemy:ranger:012', { factionNames, localFactionId: faction })).toBe('Northwind · Ranger #12')
    expect(entityName(`${faction}:castle`, { factionNames })).toBe('Iron Legion · Castle')
    expect(entityName(`${faction}:barracks:starting`, { factionNames })).toBe('Iron Legion · Barracks (starting)')
    expect(entityName(`${faction}:defense_tower:003`, { factionNames })).toBe('Iron Legion · Defense Tower #3')
    expect(entityName('realm:faction:catapult:000', { localFactionId: 'realm:faction' })).toBe('Catapult #0')
  })

  it('keeps historical labels stable without a live entity index', () => {
    const entity = { id, type: 'ranger', factionId: faction }
    const peer = { id: `${faction}:vanguard:013`, type: 'vanguard', factionId: faction }
    for (const entries of [[entity, peer], [peer, entity], [peer], []]) {
      const entities = new Map(entries.map((item) => [item.id, item]))
      expect(entityName(id, { entities, factionNames })).toBe('Iron Legion · Ranger #12')
    }
    expect(entityName(id, { factionNames })).toBe('Iron Legion · Ranger #12')
    expect(entityIndex({ units: null, buildings: null }).size).toBe(0)
    expect(entityIndex().size).toBe(0)
  })

  it('uses metadata for opaque IDs and safe labels for unknown entities and types', () => {
    const uuid = 'a71b51a4-b532-41b8-96aa-c7f7efed83e4'
    const entities = new Map([[uuid, { id: uuid, type: 'new_scout', factionId: faction }]])
    expect(entityName(uuid, { entities, factionNames })).toBe('Iron Legion · New Scout · a71b51a4')
    expect(entityName(uuid)).toBe('Entity · a71b51a4')
    expect(entityName('missing:herbalist:009')).toBe('Faction · missing · Herbalist #9')
    expect(entityName('enemy:new_scout:004', { factionNames })).toBe('Northwind · New Scout #4')
    expect(entityName('a::002')).toBe('Entity · a::002')
    expect(entityName(undefined)).toBe('Unknown entity')
    expect(typeLabel('battering_ram')).toBe('Battering Ram')
    expect(typeLabel('new-scout')).toBe('New Scout')
  })

  it('formats structured references without altering prose or unrelated IDs', () => {
    for (const field of ['unitId', 'attackerId', 'targetId', 'sourceId', 'healerId', 'attackTargetId', 'healTargetId']) {
      expect(eventStringLabel(field, id, { factionNames })).toBe('Iron Legion · Ranger #12')
    }
    expect(eventStringLabel('unitType', 'battering_ram', {})).toBe('Battering Ram')
    expect(eventStringLabel('factionId', faction, { factionNames })).toBe('Iron Legion')
    expect(eventStringLabel('destroyedByFactionId', faction, { factionNames })).toBe('Iron Legion')
    expect(eventStringLabel('factionId', 'unknown', {})).toBe('unknown')
    expect(eventStringLabel('rationale', faction, { factionNames })).toBe(faction)
    expect(eventStringLabel('requestId', id, { factionNames })).toBe(id)
  })
})
