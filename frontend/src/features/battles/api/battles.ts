// Defines battle API payloads and requests for scenarios, model profiles, battle
// creation/history, live state, events, decisions, and controls.

import { createApiClient } from '../../../shared/api/request'

export interface ScenarioSummary {
  allowedUnits?: string[]
  allowedUpgrades?: string[]
  allowedBuildings?: string[]
  id: string
  rulesVersion: string
  tickDuration: string
  decisionIntervalTicks: number
  rulesetId: string
  maximumTicks: number
  mapWidth: number
  mapHeight: number
}

export interface CreateBattleInput {
  id: string
  scenarioId: string
  participants: [BattleParticipantInput, BattleParticipantInput]
  tickCount: number
}

export interface BattleController { type: 'scripted' | 'model'; profileId?: string; provider?: string; runtime?: string; modelIdentifier?: string }
export interface BattleParticipantInput { factionId: string; controller: { type: 'scripted' } | { type: 'model'; profileId: string } }
export interface ModelProfile {
  id: string; displayName: string; provider: string; runtime: 'api' | 'cli'; scope: 'local' | 'cluster'
  modelLabel: string; available: boolean; toolCapability: boolean; unavailableReason?: string
}

export type BattleStatus = 'queued' | 'running' | 'completed' | 'failed' | 'stopped'

export interface BattleParticipant {
  id: string
  name: string
  controller?: BattleController
}

export interface FactionStatistics {
  factionId: string
  castleHealth: number
  unitsStarted: number
  unitsRecruited: number
  unitsLost: number
  unitsRemaining: number
  damageDealt: number
  damageTaken: number
  attackTicks: number
  defendTicks: number
  goldEarned: number
  goldSpent: number
  goldLooted: number
  buildingsConstructed: number
  buildingsDestroyed: number
}

export interface HexPosition { x: number; y: number }
export interface BattleMap {
  kind: 'authored'; width: number; height: number; rows: string[]
  castleSpawns: [HexPosition, HexPosition]
  economySites: [HexPosition[], HexPosition[]]
}
export interface BattleUnit {
  id: string; factionId: string; type: string; position: HexPosition
  health: number; maxHealth: number; attack: number; defense: number
  speed: number; movement: number; range: number; vision: number; readyAtTick: number
  movedFrom?: HexPosition
  attackTargetId?: string
  attackTargetPosition?: HexPosition
}
export interface BattleBuilding {
  id: string; factionId: string; type: 'castle' | 'economy'; position: HexPosition
  health: number; maxHealth: number; defense: number; vision: number; income: number
}
export interface BattleFactionState {
  ownedUpgradeIds?: string[]
  factionId: string; gold: number; goldEarned: number; goldSpent: number; goldLooted: number
}
export interface BattleState {
  openingComplete?: boolean
  battleId: string; tick: number; factions: BattleFactionState[]
  units: BattleUnit[]; buildings: BattleBuilding[]
}
export interface BattleView { map: BattleMap; state: BattleState; visiblePositions: HexPosition[] }

export interface BattleResult {
  winnerFactionId: string | null
  reason: string
  ticksRun: number
  factions: FactionStatistics[]
}

export interface BattleRun {
  id: string
  scenarioId: string
  scenarioDigest: string
  requestedTicks: number
  currentTick: number
  status: BattleStatus
  paused: boolean
  attempt: number
  participants: BattleParticipant[]
  result: BattleResult | null
  map: BattleMap
  state: BattleState
  errorCode?: string
  errorMessage?: string
}

export async function getBattleView(id: string, factionId: string, signal?: AbortSignal): Promise<BattleView> {
  const response = await battlefieldFetch(`/v1/battles/${encodeURIComponent(id)}/factions/${encodeURIComponent(factionId)}/view`, { signal })
  if (response.ok) return (await response.json()) as BattleView
  if (response.status === 400) throw new BattleApiError('That faction perspective is invalid.', 400)
  if (response.status === 404) throw new BattleApiError('This battle could not be found.', 404)
  throw new BattleApiError('Battle perspective could not be loaded.', response.status)
}

export type JsonValue = null | boolean | number | string | JsonValue[] | { [key: string]: JsonValue }

export interface BattleEvent {
  attempt: number
  sequence: number
  tick: number
  type: string
  data: Record<string, JsonValue>
  createdAt: string
}

export interface BattleEventPage {
  attempt: number
  events: BattleEvent[]
  nextCursor: number | null
}

export interface DecisionHistory {
  requestId: string; factionId: string; targetTick: number; status: 'pending' | 'running' | 'accepted' | 'fallback' | 'failed'
  controllerType: 'scripted' | 'model'
  profileId?: string; provider?: string; runtime?: string; modelIdentifier?: string
  fallbackSource?: string; errorCategory?: string
  resolvedPlan?: {
    name?: string
    rationale?: string
    actionWeights?: Record<string, number>
    targetBonuses?: Record<string, number>
    reserveGold?: number
    spendOrder?: string[]
    constructionPriorities?: HexPosition[]
    recruitmentPriorities?: string[]
    upgradeOrders?: string[]
    recruitmentOrders?: { unitType: string; count: number }[] | null
  }
  usage: { inputTokens?: number; outputTokens?: number; requests?: number }
  createdAt: string; resolvedAt?: string
}

export class BattleApiError extends Error {
  constructor(
    message: string,
    public readonly status?: number,
  ) {
    super(message)
    this.name = 'BattleApiError'
  }
}

const battlefieldFetch = createApiClient(BattleApiError)

export async function listScenarios(signal?: AbortSignal): Promise<ScenarioSummary[]> {
  const response = await battlefieldFetch('/v1/scenarios', { signal })
  if (!response.ok) {
    throw new BattleApiError('Battle scenarios could not be loaded.', response.status)
  }
  const body = (await response.json()) as { scenarios: ScenarioSummary[] }
  return body.scenarios
}

export async function listModelProfiles(signal?: AbortSignal): Promise<ModelProfile[]> {
  const response = await battlefieldFetch('/v1/model-profiles', { signal })
  if (!response.ok) throw new BattleApiError('Model profiles could not be loaded.', response.status)
  const body = (await response.json()) as { profiles: ModelProfile[] }
  return body.profiles
}

export async function createBattle(input: CreateBattleInput): Promise<{ id: string; status: BattleStatus }> {
  const response = await battlefieldFetch('/v1/battles', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
  if (response.ok) {
    return (await response.json()) as { id: string; status: BattleStatus }
  }
  if (response.status === 409) {
    throw new BattleApiError('That battle identifier already exists. Generate a new one and try again.', 409)
  }
  if (response.status === 404) {
    throw new BattleApiError('A selected faction or scenario no longer exists. Reload the deployment data.', 404)
  }
  if (response.status === 400) {
    throw new BattleApiError('The battle configuration was rejected. Review the factions and tick limit.', 400)
  }
  if (response.status === 422) {
    throw new BattleApiError('Both factions must use the scenario ruleset.', 422)
  }
  throw new BattleApiError('The battle could not be queued. Please try again.', response.status)
}

export async function getBattle(id: string, signal?: AbortSignal): Promise<BattleRun> {
  const response = await battlefieldFetch(`/v1/battles/${encodeURIComponent(id)}`, { signal })
  if (response.ok) return (await response.json()) as BattleRun
  if (response.status === 404) {
    throw new BattleApiError('This battle could not be found.', 404)
  }
  throw new BattleApiError('Battle status could not be loaded.', response.status)
}

async function controlBattle(id: string, action: 'stop' | 'pause' | 'resume'): Promise<BattleRun> {
  const response = await battlefieldFetch(`/v1/battles/${encodeURIComponent(id)}/${action}`, { method: 'POST' })
  if (response.ok) return (await response.json()) as BattleRun
  if (response.status === 404) throw new BattleApiError('This battle could not be found.', 404)
  if (response.status === 409) throw new BattleApiError('This battle has already ended.', 409)
  if (response.status === 400) throw new BattleApiError('That battle identifier is invalid.', 400)
  const outcome = { stop: 'stopped', pause: 'paused', resume: 'resumed' }[action]
  throw new BattleApiError(`The battle could not be ${outcome}.`, response.status)
}

export function stopBattle(id: string): Promise<BattleRun> {
  return controlBattle(id, 'stop')
}

export function pauseBattle(id: string): Promise<BattleRun> {
  return controlBattle(id, 'pause')
}

export function resumeBattle(id: string): Promise<BattleRun> {
  return controlBattle(id, 'resume')
}

export async function listBattleEvents(
  id: string,
  options: { attempt: number; after?: number; limit?: number; latest?: boolean },
  signal?: AbortSignal,
): Promise<BattleEventPage> {
  const query = new URLSearchParams({
    attempt: String(options.attempt),
    after: String(options.after ?? 0),
    limit: String(options.limit ?? 50),
  })
  if (options.latest) query.set('latest', 'true')
  const response = await battlefieldFetch(
    `/v1/battles/${encodeURIComponent(id)}/events?${query.toString()}`,
    { signal },
  )
  if (response.ok) return (await response.json()) as BattleEventPage
  if (response.status === 404) throw new BattleApiError('This battle could not be found.', 404)
  if (response.status === 400) throw new BattleApiError('That battle attempt or event cursor is invalid.', 400)
  throw new BattleApiError('Battle events could not be loaded.', response.status)
}

export async function listDecisionHistory(id: string, attempt: number, signal?: AbortSignal): Promise<DecisionHistory[]> {
  if (attempt < 1) return []
  const response = await battlefieldFetch(`/v1/battles/${encodeURIComponent(id)}/decisions?attempt=${attempt}`, { signal })
  if (!response.ok) throw new BattleApiError('Decision history could not be loaded.', response.status)
  return ((await response.json()) as { decisions: DecisionHistory[] }).decisions
}

export interface BattleSummary {
  id: string
  scenarioId: string
  participants: BattleParticipant[]
  status: BattleStatus
  paused: boolean
  requestedTicks: number
  currentTick: number
  createdAt: string
  startedAt: string | null
  completedAt: string | null
  outcome: { winnerFactionId: string | null; reason: string } | null
}
export interface BattleHistoryPage {
  battles: BattleSummary[]
  nextCursor: string | null
}
export async function listBattles(query: { status?: string; cursor?: string; limit?: number }, signal?: AbortSignal): Promise<BattleHistoryPage> {
  const params = new URLSearchParams()
  if (query.status) params.set('status', query.status)
  if (query.cursor) params.set('cursor', query.cursor)
  params.set('limit', String(query.limit ?? 25))
  const response = await battlefieldFetch(`/v1/battles?${params}`, { signal })
  if (!response.ok) throw new BattleApiError(response.status === 400 ? 'That battle history query is invalid.' : 'Battle history could not be loaded.', response.status)
  return await response.json() as BattleHistoryPage
}
