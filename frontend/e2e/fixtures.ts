import { test as base, expect } from '@playwright/test'

export const factionId = '11111111-1111-4111-8111-111111111111'
export const rivalId = '22222222-2222-4222-8222-222222222222'
export const battleId = '33333333-3333-4333-8333-333333333333'
export const factions = [{ id: factionId, name: 'Amber', doctrine: 'Protect the castle.', background: '', ownerId: 'admin' }, { id: rivalId, name: 'Azure', doctrine: 'Advance.', background: '', ownerId: 'admin' }]
export function battleFixture(width = 15, height = 11) {
  const map = { kind: 'authored', width, height, rows: Array<string>(height).fill('.'.repeat(width)), castleSpawns: [{ x: 1, y: 1 }, { x: width - 2, y: height - 2 }], economySites: [[], []] }
  const state = { battleId, tick: 89, factions: factions.map(faction => ({ factionId: faction.id, gold: 100, goldEarned: 100, goldSpent: 0, goldLooted: 0 })), units: [], buildings: factions.map((faction, index) => ({ id: `${faction.id}:castle`, factionId: faction.id, type: 'castle', position: map.castleSpawns[index], health: index ? 0 : 300, maxHealth: 300, defense: 8, vision: 4, income: 10 })) }
  return { id: battleId, ownerId: 'admin', scenarioId: 'castle-siege-v1', scenarioDigest: 'fixture', status: 'completed', paused: false, currentTick: 89, requestedTicks: 500, attempt: 1, participants: factions, map, state, result: { winnerFactionId: factionId, reason: 'castle_destroyed', ticksRun: 89, factions: [] } }
}
type FixtureState = { deleteFails: boolean; deleted: boolean; generation: Record<string, unknown>; writes: string[]; unexpected: string[]; width: number; height: number }
export const test = base.extend<{ api: FixtureState }>({
  api: [async ({ page }, use) => {
    const api: FixtureState = { deleteFails: false, deleted: false, generation: { available: false, reason: 'Disabled by operator', remaining: 10, modelLabel: '', resetAt: '2099-01-01T00:00:00Z', retryAt: '' }, writes: [], unexpected: [], width: 15, height: 11 }
    await page.route('**/v1/**', async route => {
      const { pathname } = new URL(route.request().url())
      const method = route.request().method()
      const reply = (json: unknown) => route.fulfill({ json })
      if (method !== 'GET') api.writes.push(`${method} ${pathname}`)
      if (pathname === '/v1/auth/session') return reply({ enabled: true, authenticated: true, actor: { id: 'admin', role: 'admin' } })
      if (pathname === '/v1/auth/logout') return route.fulfill({ status: 204 })
      if (pathname === `/v1/factions/${factionId}` && method === 'DELETE') {
        if (api.deleteFails) return route.fulfill({ status: 500, body: 'Failed' })
        api.deleted = true
        return route.fulfill({ status: 204 })
      }
      if (pathname === '/v1/factions') return reply({ factions: api.deleted ? [factions[1]] : factions })
      if (pathname === '/v1/faction-background-generations/options') return reply(api.generation)
      if (pathname === '/v1/scenarios') return reply({ scenarios: [{ id: 'castle-siege-v1', rulesVersion: '1', rulesetId: 'castle-siege-v1', mapWidth: 15, mapHeight: 11, maximumTicks: 500, tickDuration: '1s', decisionIntervalTicks: 10 }] })
      if (pathname === '/v1/model-profiles') return reply({ profiles: [{ id: 'offline', displayName: 'Offline AI', available: false, runtime: 'api', modelLabel: '', unavailableReason: 'Disabled by operator' }] })
      const battle = battleFixture(api.width, api.height)
      if (pathname === '/v1/battles') return reply({ battles: [{ ...battle, createdAt: '2026-09-10T00:00:00Z', outcome: { winnerFactionId: factionId, reason: 'castle_destroyed' } }], nextCursor: '' })
      if (pathname.endsWith('/view')) return reply({ map: battle.map, state: battle.state, visiblePositions: battle.map.rows.flatMap((row, y) => [...row].map((_, x) => ({ x, y }))) })
      if (pathname === `/v1/battles/${battleId}`) return reply(battle)
      if (pathname.endsWith('/decisions')) return reply({ decisions: [] })
      if (pathname.endsWith('/events')) return reply({ attempt: 1, nextCursor: 14, events: Array.from({ length: 14 }, (_, index) => ({ attempt: 1, sequence: index + 1, tick: 89, type: index === 0 ? 'future_event' : index === 1 ? 'unit_moved' : 'unit_recruited', data: { factionId, unitType: 'ranger' }, createdAt: '2026-09-10T00:00:00Z' })) })
      api.unexpected.push(`${method} ${pathname}`)
      return route.fulfill({ status: 404, body: 'Unexpected test request' })
    })
    await use(api)
    expect(api.unexpected).toEqual([])
  }, { auto: true }],
})
export { expect }
