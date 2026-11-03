// League API payloads and requests: create, list, read and cancel AI leagues, and
// disqualify their entrants.

import { createApiClient, errorMessage } from '../../../shared/api/request'
import type { LeagueMatch } from '../../../shared/domain/leagueMatches'
import type { BattleController } from '../../battles/api/battles'

export type LeagueStatus = 'running' | 'completed' | 'cancelled'
export type LeagueStage = 'group' | 'quarterfinal' | 'semifinal' | 'final'
export type FixtureStatus = 'pending' | 'running' | 'completed' | 'void' | 'cancelled' | 'forfeited'
export type GroupLetter = 'A' | 'B' | 'C' | 'D'

export interface LeagueSettings { scenarioIds: string[]; tickCount: number; tickDuration?: string; decisionIntervalTicks?: number }
export interface LeagueProgress { settled: number; total: number }
/** Who removed an entrant from its league, when, and the reason given ('' for none). */
export interface LeagueDisqualification { at: string; by: string; reason: string }
export interface LeagueEntrant {
  index: number; group: GroupLetter; factionId: string; factionName: string; controller: BattleController
  /** True once the entrant can no longer win the league. */
  eliminated: boolean
  disqualification: LeagueDisqualification | null
}
/** An entrant's group record; placement is null until the group decides it. */
export interface LeagueStanding { entrant: number; placement: number | null; won: number; lost: number }
export interface LeagueTie { stage: LeagueStage; tie: number; homeEntrant: number; awayEntrant: number; winner: number | null }
export interface LeagueFixture {
  id: string; sequence: number; stage: LeagueStage; group?: GroupLetter; round: number; tie: number
  /** A group fixture's match; absent in the knockout. */
  match?: LeagueMatch
  homeEntrant: number; awayEntrant: number; scenarioId: string; status: FixtureStatus; attempts: number; battleId: string | null
  /** Present while the fixture runs: its battle's tick and tick limit. */
  battleProgress?: { currentTick: number; requestedTicks: number }
  /** Present once completed: the battle's winner, null for a draw. */
  result?: { winner: number | null; reason: string }
  /** Present once forfeited: the disqualified side, or both. */
  forfeit?: 'home' | 'away' | 'both'
  /** The match winner once completed, void or forfeited, by tiebreak when the battle had none. */
  winner: number | null
}
export interface League {
  id: string; ownerId: string; name: string; status: LeagueStatus; groupCount: 2 | 4; settings: LeagueSettings
  champion: number | null; progress: LeagueProgress
  /** The rules versions of the fixtures' battles, newest first; empty before the first battle. */
  rulesVersions: string[]
  entrants: LeagueEntrant[]
  groups: { group: GroupLetter; standings: LeagueStanding[] }[]; knockout: LeagueTie[]; fixtures: LeagueFixture[]
  createdAt: string; completedAt: string | null
}
export interface LeagueSummary {
  id: string; ownerId: string; name: string; status: LeagueStatus; groupCount: 2 | 4; entrantCount: 8 | 16
  champion: { entrant: number; factionName: string } | null; progress: LeagueProgress; rulesVersions: string[]; createdAt: string; completedAt: string | null
}
export interface CreateLeagueInput {
  id: string; name: string; scenarioIds: string[]; tickCount: number; tickDuration?: string; decisionIntervalTicks?: number
  entrants: { factionId: string; group: GroupLetter; controller: { type: 'scripted' } | { type: 'model'; profileId: string; modelIdentifier?: string; reasoningEffort?: string } }[]
}

export class LeagueApiError extends Error {
  constructor(message: string, public readonly status?: number) {
    super(message)
    this.name = 'LeagueApiError'
  }
}

const leagueFetch = createApiClient(LeagueApiError)

/** The capacity league battles run in: engine battle slots and shared CLI execution slots. */
export interface LeagueScheduling { battleSlots: number; cliSlots: number }
export interface LeagueList { leagues: LeagueSummary[]; scheduling?: LeagueScheduling }

/** Lists leagues, newest first; a rules version keeps only leagues with a battle on it. */
export async function listLeagues(signal?: AbortSignal, query: { rulesVersion?: string } = {}): Promise<LeagueList> {
  const params = query.rulesVersion ? `?${new URLSearchParams({ rulesVersion: query.rulesVersion })}` : ''
  const response = await leagueFetch(`/v1/leagues${params}`, { signal })
  if (!response.ok) throw new LeagueApiError('Leagues could not be loaded.', response.status)
  return (await response.json()) as LeagueList
}

export async function getLeague(id: string, signal?: AbortSignal): Promise<League> {
  const response = await leagueFetch(`/v1/leagues/${encodeURIComponent(id)}`, { signal })
  if (response.ok) return (await response.json()) as League
  if (response.status === 404) throw new LeagueApiError('This league could not be found.', 404)
  throw new LeagueApiError('The league could not be loaded.', response.status)
}

const createErrors: Record<number, string> = {
  400: 'The league was rejected. Check the name, maps, timing and that every group has four different factions.',
  403: 'Only administrators can start a league.',
  404: 'A selected faction or map no longer exists. Reload and try again.',
  409: 'That league identifier is already in use. Reload the form and try again.',
  422: 'An entrant cannot play on a selected map, or its model is unavailable.',
}

export async function createLeague(input: CreateLeagueInput): Promise<League> {
  const response = await leagueFetch('/v1/leagues', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) })
  if (response.ok) return (await response.json()) as League
  throw new LeagueApiError(createErrors[response.status] ?? 'The league could not be started. Please try again.', response.status)
}

export async function cancelLeague(id: string): Promise<League> {
  const response = await leagueFetch(`/v1/leagues/${encodeURIComponent(id)}/cancel`, { method: 'POST' })
  if (response.ok) return (await response.json()) as League
  if (response.status === 409) throw new LeagueApiError('This league has already finished.', 409)
  throw new LeagueApiError('The league could not be cancelled.', response.status)
}

const disqualifyErrors: Record<number, string> = {
  400: 'The reason is too long. Keep it to 200 characters.',
  403: 'Only administrators can disqualify entrants.',
  404: 'This entrant or league no longer exists. Reload and try again.',
}

/** Removes an entrant from a running league; a 409 carries the server's reason it cannot. */
export async function disqualifyEntrant(id: string, entrant: number, reason: string): Promise<League> {
  const response = await leagueFetch(`/v1/leagues/${encodeURIComponent(id)}/entrants/${entrant}/disqualify`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ reason }) })
  if (response.ok) return (await response.json()) as League
  const detail = response.status === 409 ? await errorMessage(response) : ''
  throw new LeagueApiError(disqualifyErrors[response.status] ?? (detail ? `The entrant cannot be disqualified: ${detail}.` : 'The entrant could not be disqualified.'), response.status)
}
