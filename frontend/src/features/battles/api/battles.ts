const API_URL = (import.meta.env.VITE_API_URL ?? '').replace(/\/$/, '')

export interface ScenarioSummary {
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
  factionId: string; gold: number; goldEarned: number; goldSpent: number; goldLooted: number
}
export interface BattleState {
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

async function battlefieldFetch(path: string, init?: RequestInit): Promise<Response> {
  try {
    return await apiFetch(`${API_URL}${path}`, init)
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    throw new BattleApiError(
      'The command post is unreachable. Check that the battlefield server is running.',
    )
  }
}

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

export async function stopBattle(id: string): Promise<BattleRun> {
  const response = await battlefieldFetch(`/v1/battles/${encodeURIComponent(id)}/stop`, { method: 'POST' })
  if (response.ok) return (await response.json()) as BattleRun
  if (response.status === 404) throw new BattleApiError('This battle could not be found.', 404)
  if (response.status === 409) throw new BattleApiError('This battle has already ended.', 409)
  if (response.status === 400) throw new BattleApiError('That battle identifier is invalid.', 400)
  throw new BattleApiError('The battle could not be stopped.', response.status)
}

async function setBattlePaused(id: string, action: 'pause' | 'resume'): Promise<BattleRun> {
  const response = await battlefieldFetch(`/v1/battles/${encodeURIComponent(id)}/${action}`, { method: 'POST' })
  if (response.ok) return (await response.json()) as BattleRun
  if (response.status === 404) throw new BattleApiError('This battle could not be found.', 404)
  if (response.status === 409) throw new BattleApiError('This battle has already ended.', 409)
  if (response.status === 400) throw new BattleApiError('That battle identifier is invalid.', 400)
  throw new BattleApiError(`The battle could not be ${action === 'pause' ? 'paused' : 'resumed'}.`, response.status)
}

export function pauseBattle(id: string): Promise<BattleRun> {
  return setBattlePaused(id, 'pause')
}

export function resumeBattle(id: string): Promise<BattleRun> {
  return setBattlePaused(id, 'resume')
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
import { apiFetch } from '../../../shared/api/request'
