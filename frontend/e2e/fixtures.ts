import { test as base, expect, type Route } from '@playwright/test'

export const factionId = '11111111-1111-4111-8111-111111111111'
export const rivalId = '22222222-2222-4222-8222-222222222222'
export const battleId = '33333333-3333-4333-8333-333333333333'
export const factions = [{ id: factionId, name: 'Amber', doctrine: 'Protect the castle.', background: '', ownerId: 'admin', ownerName: 'Admin' }, { id: rivalId, name: 'Azure', doctrine: 'Advance.', background: '', ownerId: 'admin', ownerName: 'Admin' }]
export function battleFixture(width = 15, height = 11) {
  const map = { kind: 'authored', width, height, rows: Array<string>(height).fill('.'.repeat(width)), castleSpawns: [{ x: 1, y: 1 }, { x: width - 2, y: height - 2 }], economySites: [[], []] }
  const state = { battleId, tick: 89, factions: factions.map(faction => ({ factionId: faction.id, gold: 100, goldEarned: 100, goldSpent: 0, goldLooted: 0 })), units: [], buildings: factions.map((faction, index) => ({ id: `${faction.id}:castle`, factionId: faction.id, type: 'castle', position: map.castleSpawns[index], health: index ? 0 : 300, maxHealth: 300, defense: 8, vision: 4, income: 10 })) }
  return { id: battleId, ownerId: 'admin', ownerName: 'Admin', scenarioId: 'castle-siege-v1', scenarioDigest: 'fixture', status: 'completed', paused: false, currentTick: 89, requestedTicks: 500, attempt: 1, participants: factions, map, state, result: { winnerFactionId: factionId, reason: 'castle_destroyed', ticksRun: 89, factions: [] } }
}
// Ten-tick segments of the completed fixture battle, each tick collecting income.
function replayFixture(battle: ReturnType<typeof battleFixture>, tick: number) {
  const fromTick = Math.floor(tick / 10) * 10
  const toTick = Math.min(fromTick + 9, battle.currentTick)
  return {
    battleId, attempt: 1, finalTick: battle.currentTick, fromTick, toTick, nextTick: toTick < battle.currentTick ? toTick + 1 : null,
    frames: Array.from({ length: toTick - fromTick + 1 }, (_, index) => ({
      tick: fromTick + index, state: { ...battle.state, tick: fromTick + index },
      events: [{ sequence: fromTick + index + 1, tick: fromTick + index, type: 'income_collected', data: { factionId, amount: 10 } }],
    })),
  }
}
type CreatedBattle = { id: string; scenarioId: string; participants: { factionId: string; controller: { type: string; profileId?: string } }[]; tickCount: number }
type Session = { enabled: boolean; authenticated: boolean; mode?: string; actor: { id: string; role: string; displayName?: string } }
type FixtureState = { session: Session; takenNames: string[]; deleteFails: boolean; deleted: boolean; createFails: boolean; created: CreatedBattle | null; generation: Record<string, unknown>; writes: string[]; unexpected: string[]; width: number; height: number }

// Battle reads, kept out of the main router so each handler stays simple to read.
function battleRoute(api: FixtureState, pathname: string, search: URLSearchParams): unknown {
  const battle = battleFixture(api.width, api.height)
  if (pathname === '/v1/battles') return { battles: [{ ...battle, createdAt: '2026-09-10T00:00:00Z', outcome: { winnerFactionId: factionId, reason: 'castle_destroyed' } }], nextCursor: '' }
  if (pathname === `/v1/battles/${battleId}`) return battle
  // A freshly staged battle is served as queued under its own generated identifier.
  if (api.created && pathname === `/v1/battles/${api.created.id}`) {
    return { ...battle, id: api.created.id, status: 'queued', paused: false, currentTick: 0, requestedTicks: api.created.tickCount, result: null }
  }
  if (pathname.endsWith('/decisions')) return { decisions: [] }
  if (pathname === `/v1/battles/${battleId}/replay`) return replayFixture(battle, Number(search.get('tick') ?? 0))
  if (pathname.endsWith('/events')) return { attempt: 1, nextCursor: 14, events: Array.from({ length: 14 }, (_, index) => ({ attempt: 1, sequence: index + 1, tick: 89, type: index === 0 ? 'future_event' : index === 1 ? 'unit_moved' : 'unit_recruited', data: { factionId, unitType: 'ranger' }, createdAt: '2026-09-10T00:00:00Z' })) }
  return null
}
export const test = base.extend<{ api: FixtureState }>({
  api: [async ({ page }, use) => {
    const api: FixtureState = { session: { enabled: true, authenticated: true, actor: { id: 'admin', role: 'admin' } }, takenNames: [], deleteFails: false, deleted: false, createFails: false, created: null, generation: { available: false, reason: 'Disabled by operator', remaining: 10, modelLabel: '', resetAt: '2099-01-01T00:00:00Z', retryAt: '' }, writes: [], unexpected: [], width: 15, height: 11 }
    // Mutating routes are kept separate so each handler stays simple to read.
    const writeRoute = async (route: Route, pathname: string, method: string) => {
      if (pathname === '/v1/auth/logout') return route.fulfill({ status: 204 })
      if (pathname === '/v1/profile' && method === 'PUT') {
        const displayName = (route.request().postDataJSON() as { displayName: string }).displayName.trim()
        if (api.takenNames.includes(displayName.toLowerCase())) return route.fulfill({ status: 409, json: { error: { code: 'conflict', message: 'that display name is taken' } } })
        api.session = { ...api.session, actor: { ...api.session.actor, displayName } }
        return route.fulfill({ json: { displayName } })
      }
      if (pathname === `/v1/factions/${factionId}` && method === 'DELETE') {
        if (api.deleteFails) return route.fulfill({ status: 500, body: 'Failed' })
        api.deleted = true
        return route.fulfill({ status: 204 })
      }
      if (pathname === '/v1/battles' && method === 'POST') {
        if (api.createFails) return route.fulfill({ status: 422, body: 'Ruleset mismatch' })
        api.created = route.request().postDataJSON() as CreatedBattle
        return route.fulfill({ status: 201, json: { id: api.created.id, status: 'queued' } })
      }
      return null
    }
    await page.route('**/v1/**', async route => {
      const { pathname, searchParams } = new URL(route.request().url())
      const method = route.request().method()
      const reply = (json: unknown) => route.fulfill({ json })
      if (method !== 'GET') {
        api.writes.push(`${method} ${pathname}`)
        const handled = await writeRoute(route, pathname, method)
        if (handled !== null) return handled
      }
      if (pathname === '/v1/auth/session') return reply(api.session)
      if (pathname === '/v1/factions') return reply({ factions: api.deleted ? [factions[1]] : factions })
      if (pathname === '/v1/faction-background-generations/options') return reply(api.generation)
      if (pathname === '/v1/scenarios') return reply({ scenarios: [{ id: 'castle-siege', rulesVersion: '3', rulesetId: 'battlefield', mapWidth: 15, mapHeight: 11, maximumTicks: 800, tickDuration: '1s', decisionIntervalTicks: 10, map: { kind: 'authored', width: 15, height: 11, rows: Array<string>(11).fill('..g....f....#..'), castleSpawns: [{ x: 1, y: 5 }, { x: 13, y: 5 }], economySites: [[], []] } }] })
      if (pathname === '/v1/model-profiles') return reply({ profiles: [{ id: 'offline', displayName: 'Offline AI', available: false, runtime: 'api', modelLabel: '', unavailableReason: 'Disabled by operator' }] })
      if (pathname === '/v1/battles/allowance') return reply({ limited: false, startedToday: 0, unfinished: 0, serviceUnfinished: 0, resetAt: '2099-01-01T00:00:00Z' })
      const battleReply = battleRoute(api, pathname, searchParams)
      if (battleReply !== null) return reply(battleReply)
      api.unexpected.push(`${method} ${pathname}`)
      return route.fulfill({ status: 404, body: 'Unexpected test request' })
    })
    await use(api)
    expect(api.unexpected).toEqual([])
  }, { auto: true }],
})
export { expect }
