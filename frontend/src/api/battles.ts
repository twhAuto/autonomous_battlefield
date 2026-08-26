const API_URL = (import.meta.env.VITE_API_URL ?? '').replace(/\/$/, '')

export interface ScenarioSummary {
  id: string
  rulesVersion: string
  tickDuration: string
  decisionIntervalTicks: number
  decisionLeadTicks: number
}

export interface CreateBattleInput {
  id: string
  scenarioId: string
  factionIds: [string, string]
  tickCount: number
}

export type BattleStatus = 'queued' | 'running' | 'completed' | 'failed'

export interface BattleParticipant {
  id: string
  name: string
}

export interface FactionStatistics {
  factionId: string
  unitsStarted: number
  unitsRemaining: number
  damageDealt: number
  damageTaken: number
  attackTicks: number
  defendTicks: number
}

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
  attempt: number
  participants: BattleParticipant[]
  result: BattleResult | null
  errorCode?: string
  errorMessage?: string
}

export interface BattleEvent {
  attempt: number
  sequence: number
  tick: number
  type: string
  data: Record<string, unknown>
  createdAt: string
}

export interface BattleEventPage {
  attempt: number
  events: BattleEvent[]
  nextCursor: number | null
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
    return await fetch(`${API_URL}${path}`, init)
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

export async function listBattleEvents(
  id: string,
  options: { attempt: number; after?: number; limit?: number },
  signal?: AbortSignal,
): Promise<BattleEventPage> {
  const query = new URLSearchParams({
    attempt: String(options.attempt),
    after: String(options.after ?? 0),
    limit: String(options.limit ?? 50),
  })
  const response = await battlefieldFetch(
    `/v1/battles/${encodeURIComponent(id)}/events?${query.toString()}`,
    { signal },
  )
  if (response.ok) return (await response.json()) as BattleEventPage
  if (response.status === 404) throw new BattleApiError('This battle could not be found.', 404)
  if (response.status === 400) throw new BattleApiError('That battle attempt or event cursor is invalid.', 400)
  throw new BattleApiError('Battle events could not be loaded.', response.status)
}
