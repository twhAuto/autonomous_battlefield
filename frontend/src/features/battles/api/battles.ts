// Defines battle API payloads and requests for scenarios, model profiles, battle
// creation/history, live state, events, replays, decisions, and controls.

import type { UnitCapabilities } from '../../../shared/domain/units'
import type { ModelProfile } from '../../../shared/domain/model-profile'
import type { MatchPlace } from '../../../shared/domain/leagueMatches'
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
  map?: BattleMap
}

export interface CreateBattleInput {
  id: string
  scenarioId: string
  participants: [BattleParticipantInput, BattleParticipantInput]
  tickCount: number
  tickDuration?: string
  decisionIntervalTicks?: number
}

export interface BattleController { type: 'scripted' | 'model'; profileId?: string; provider?: string; runtime?: string; reasoningEffort?: string; modelIdentifier?: string }
export interface BattleParticipantInput { factionId: string; controller: { type: 'scripted' } | { type: 'model'; profileId: string; modelIdentifier?: string; reasoningEffort?: string } }

export type BattleStatus = 'queued' | 'running' | 'completed' | 'failed' | 'stopped'

export interface BattleParticipant {
  id: string
  name: string
  background?: string
  doctrine?: string
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
  /** Omitted when empty, and from results written before per-type statistics. */
  unitTypes?: UnitTypeStatistics[]
  buildingTypes?: BuildingTypeStatistics[]
  upgrades?: UpgradePurchase[]
}

/** Kills and buildingsDestroyed count destroying blows; goldSpent is the price paid to queue the type. */
export interface UnitTypeStatistics {
  unitType: string
  started: number; recruited: number; lost: number; kills: number; buildingsDestroyed: number
  damageDealt: number; damageTaken: number; healthRestored: number; goldSpent: number
}
export interface BuildingTypeStatistics {
  buildingType: string
  constructed: number; lost: number; kills: number; damageDealt: number; damageTaken: number; goldSpent: number
}
export interface UpgradePurchase { upgradeId: string; tick: number }

export interface HexPosition { x: number; y: number }
export interface BattleMap {
  kind: 'authored'; width: number; height: number; rows: string[]
  castleSpawns: [HexPosition, HexPosition]
  economySites: [HexPosition[], HexPosition[]]
  /** Gold per mine on this map; absent maps use the ruleset default. */
  goldMineReserve?: number
}
/** A gold deposit's remaining gold; present only for rulesets with finite gold mines. */
export interface GoldMine { position: HexPosition; reserve: number; remaining: number }
export interface BattleUnit extends UnitCapabilities {
  id: string; factionId: string; type: string; position: HexPosition
  health: number; maxHealth: number; attack: number; defense: number
  speed: number; movement: number; range: number; vision: number; readyAtTick: number
  healTargetId?: string
  healTargetPosition?: HexPosition
  movementPath?: HexPosition[]
  movedFrom?: HexPosition
  attackOutcome?: 'hit' | 'miss'
  attackTargetId?: string
  attackTargetPosition?: HexPosition
}
export interface BattleBuilding {
  production?: { configured?: boolean; unitType?: string; enabled: boolean; waitingForGold?: boolean; waitingForUnitLimit?: boolean; waitingForUpgrade?: boolean }
  trains?: string[]
  buildRange?: number
  trainingBlocked?: boolean
  trainingQueue?: { unitType: string; remainingTicks: number; trainingTicks: number; queuedAtTick: number; opening?: boolean }[]
  attack?: number; range?: number; readyAtTick?: number
  attackOutcome?: 'hit' | 'miss'
  attackTargetId?: string; attackTargetPosition?: HexPosition
  id: string; factionId: string; type: 'castle' | 'economy' | 'defense_tower' | 'watchtower' | 'outpost' | 'barracks' | 'archery_range' | 'stables' | 'siege_workshop' | 'infirmary'; position: HexPosition
  health: number; maxHealth: number; defense: number; vision: number; income: number
}
export interface BattleFactionState {
  ownedUpgradeIds?: string[]
  factionId: string; gold: number; goldEarned: number; goldSpent: number; goldLooted: number
}
export interface BattleState {
  goldMines?: GoldMine[]
  unitDeaths?: { id: string; factionId: string; type: string; position: HexPosition }[]
  openingComplete?: boolean
  battleId: string; tick: number; factions: BattleFactionState[] | null
  units: BattleUnit[] | null; buildings: BattleBuilding[] | null
}
export interface BattleView { map: BattleMap; state: BattleState; visiblePositions: HexPosition[] }

export interface BattleResult {
  winnerFactionId: string | null
  reason: string
  ticksRun: number
  factions: FactionStatistics[]
}

/** `opening` marks the one-time purchase before tick 1 (nextTick 0). */
export interface ProductionSchedule { nextTick: number; opening: boolean }

/** Places a league battle in its league; absent for other battles. */
export interface BattleLeague extends MatchPlace { id: string; name: string; fixtureId: string; round: number }

export interface BattleRun {
  ownerId?: string
  /** Who started the battle: their display name, Admin, or a neutral fallback; absent when names are unavailable. */
  ownerName?: string
  league?: BattleLeague
  id: string
  scenarioId: string
  scenarioDigest: string
  requestedTicks: number
  currentTick: number
  /** Per-faction cap on living plus in-training units; absent when unlimited. */
  maximumUnits?: number
  /** True when the ruleset closes construction where faction territories overlap. */
  contestedTerritory?: boolean
  /** Next spending point computed by the engine; null when settled or none remains. */
  production?: ProductionSchedule | null
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

/** An event re-created for a replay frame; Players receive an empty `data` object. */
export type ReplayEvent = Pick<BattleEvent, 'sequence' | 'tick' | 'type' | 'data'>
export interface ReplayFrame { tick: number; state: BattleState; events: ReplayEvent[] }
/** One segment: a checkpoint tick and the ticks before the next decision window. */
export interface BattleReplay {
  battleId: string; attempt: number; finalTick: number
  fromTick: number; toTick: number; nextTick: number | null
  frames: ReplayFrame[]
}

// Players receive only the request, faction, tick, status, controller type, plan and times.
export interface DecisionHistory {
  contextDelivery?: 'tool' | 'prefetch'
  cliProcessStartMs?: number; reasoningEffort?: string; claimedAt?: string; contextServedAt?: string; runnerCompletedAt?: string
  requestId: string; factionId: string; targetTick: number; status: 'pending' | 'running' | 'accepted' | 'fallback' | 'failed'
  controllerType: 'scripted' | 'model'
  profileId?: string; provider?: string; runtime?: string; modelIdentifier?: string
  // The model the provider reported running, when it named one; an alias selection
  // such as `opus` shows here as its full model ID.
  resolvedModel?: string
  fallbackSource?: string; errorCategory?: string
  resolvedPlan?: {
    name?: string
    rationale?: string
    actionWeights?: Record<string, number>
    targetBonuses?: Record<string, number>
    reserveGold?: number
    spendOrder?: string[]
    constructionOrders?: { buildingType: string; position: HexPosition }[] | null
    constructionPriorities?: HexPosition[]
    recruitmentPriorities?: string[]
    upgradeOrders?: string[]
    productionOrders?: { buildingId: string; unitType?: string; enabled: boolean }[] | null
    recruitmentOrders?: { unitType: string; count: number; sourceBuildingId?: string }[] | null
  }
  traceUrl?: string
  usage: { inputTokens?: number; outputTokens?: number; requests?: number; scope?: string; tokensReported?: boolean; cachedInputTokens?: number; reasoningOutputTokens?: number; modelCalls?: number; cliInvocations?: number }
  createdAt: string; resolvedAt?: string
}

// Saved public observation; never substitutes the live spectator state.
export interface SavedDecisionContext {
  initialContext?: JsonValue
  availableUnitDetails?: JsonValue
  requestId: string; battleId: string; factionId: string; attempt: number
  targetTick: number; observationTick: number
  context: {
    factionName?: string; background?: string; doctrine?: string; opening: boolean
    ownedUpgradeIds: string[] | null
    economy: { gold: number; projectedIncome: number; economyBuildingCost: number; economyBuildingIncome: number; currentEconomyBuildings: number; maximumEconomyBuildings: number; maximumBuildings?: number; currentBuildings?: number; maximumUnits?: number; currentUnits?: number }
    view: BattleView
    legal: { actionKinds: string[] | null; units: { id: string; cost: number }[] | null; economySites: HexPosition[] | null; goldMineSites?: HexPosition[]; buildings?: { id: string; cost: number }[] | null; upgrades?: { id: string; cost: number; track?: string; level?: number; requiresUpgradeId?: string; targetUnit?: string; targetClass?: string; exclusiveGroup?: string; movementBonus?: number; rangeBonus?: number; healAmountBonus?: number; healRangeBonus?: number; buildingDamageBonus?: number; attackBonus?: number; defenseBonus?: number; economyIncomeBonus?: number }[] | null; recruitmentSources?: string[] | null }
    activePlan: DecisionHistory['resolvedPlan']
    memory?: JsonValue
    knowledge?: JsonValue
  } | null
}

export async function getDecisionContext(id: string, requestId: string, attempt: number, signal?: AbortSignal): Promise<SavedDecisionContext> {
  const response = await battlefieldFetch(`/v1/battles/${encodeURIComponent(id)}/decisions/${encodeURIComponent(requestId)}/context?attempt=${attempt}`, { signal })
  if (response.ok) return await response.json() as SavedDecisionContext
  if (response.status === 404) throw new BattleApiError('Saved context is unavailable for this decision.', 404)
  throw new BattleApiError('Decision context could not be loaded.', response.status)
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

// BattleAllowance is the caller's battle allowance; Admins are not limited and carry no limits.
export interface BattleAllowance {
  limited: boolean
  startedToday: number
  dailyLimit?: number
  unfinished: number
  unfinishedLimit?: number
  serviceUnfinished: number
  serviceUnfinishedLimit?: number
  resetAt: string
}

export async function getBattleAllowance(signal?: AbortSignal): Promise<BattleAllowance> {
  const response = await battlefieldFetch('/v1/battles/allowance', { signal })
  if (!response.ok) throw new BattleApiError('Battle allowance could not be loaded.', response.status)
  return (await response.json()) as BattleAllowance
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
  if (response.status === 429) {
    throw new BattleApiError('Battle allowance reached. Queued, running, and paused battles count toward active limits. Check Battle history to resume or stop an unfinished battle, or try later if the daily allowance has been used.', 429)
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

export async function getBattleReplay(id: string, tick: number, signal?: AbortSignal): Promise<BattleReplay> {
  const response = await battlefieldFetch(`/v1/battles/${encodeURIComponent(id)}/replay?tick=${tick}`, { signal })
  if (response.ok) return (await response.json()) as BattleReplay
  if (response.status === 409) throw new BattleApiError('No replay was recorded for this part of the battle. Battles that finished before replays were added cannot be replayed.', 409)
  if (response.status === 404) throw new BattleApiError('This battle could not be found.', 404)
  throw new BattleApiError('The battle replay could not be loaded.', response.status)
}

export async function listDecisionHistory(id: string, attempt: number, signal?: AbortSignal): Promise<DecisionHistory[]> {
  if (attempt < 1) return []
  const response = await battlefieldFetch(`/v1/battles/${encodeURIComponent(id)}/decisions?attempt=${attempt}`, { signal })
  if (!response.ok) throw new BattleApiError('Decision history could not be loaded.', response.status)
  return ((await response.json()) as { decisions: DecisionHistory[] }).decisions
}

export interface BattleSummary {
  id: string
  /** Who started the battle: their display name, Admin, or a neutral fallback; absent when names are unavailable. */
  ownerName?: string
  scenarioId: string
  /** The rules version the battle's scenario was frozen with. */
  rulesVersion: string
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
export async function listBattles(query: { status?: string; rulesVersion?: string; cursor?: string; limit?: number }, signal?: AbortSignal): Promise<BattleHistoryPage> {
  const params = new URLSearchParams()
  if (query.status) params.set('status', query.status)
  if (query.rulesVersion) params.set('rulesVersion', query.rulesVersion)
  if (query.cursor) params.set('cursor', query.cursor)
  params.set('limit', String(query.limit ?? 25))
  const response = await battlefieldFetch(`/v1/battles?${params}`, { signal })
  if (!response.ok) throw new BattleApiError(response.status === 400 ? 'That battle history query is invalid.' : 'Battle history could not be loaded.', response.status)
  return await response.json() as BattleHistoryPage
}

/** Every rules version a battle recorded, and current, the version new battles use; newest first. */
export interface RulesVersions { current: string; versions: string[] }
export async function listRulesVersions(signal?: AbortSignal): Promise<RulesVersions> {
  const response = await battlefieldFetch('/v1/battles/rules-versions', { signal })
  if (!response.ok) throw new BattleApiError('Rules versions could not be loaded.', response.status)
  return await response.json() as RulesVersions
}
