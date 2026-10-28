// League API payloads and requests: create, list, read and cancel AI leagues.

import { createApiClient } from '../../../shared/api/request'
import type { BattleController } from '../../battles/api/battles'

export type LeagueStatus = 'running' | 'completed' | 'cancelled'
export type LeagueStage = 'group' | 'quarterfinal' | 'semifinal' | 'final'
export type FixtureStatus = 'pending' | 'running' | 'completed' | 'void' | 'cancelled'
export type GroupLetter = 'A' | 'B' | 'C' | 'D'

export interface LeagueSettings { scenarioIds: string[]; tickCount: number; tickDuration?: string; decisionIntervalTicks?: number }
export interface LeagueProgress { settled: number; total: number }
export interface LeagueEntrant { index: number; group: GroupLetter; factionId: string; factionName: string; controller: BattleController }
export interface LeagueStanding {
  entrant: number; position: number; played: number; won: number; drawn: number; lost: number; points: number
  castleDifference: number; damageDifference: number
}
export interface LeagueTie { stage: LeagueStage; tie: number; homeEntrant: number; awayEntrant: number; winner: number | null }
export interface LeagueFixture {
  id: string; sequence: number; stage: LeagueStage; group?: GroupLetter; round: number; tie?: number; leg: 1 | 2
  homeEntrant: number; awayEntrant: number; scenarioId: string; status: FixtureStatus; attempts: number; battleId: string | null
  /** Present once completed; winner is null for a draw. */
  result?: { winner: number | null; reason: string }
}
export interface League {
  id: string; ownerId: string; name: string; status: LeagueStatus; groupCount: 2 | 4; settings: LeagueSettings
  champion: number | null; progress: LeagueProgress; entrants: LeagueEntrant[]
  groups: { group: GroupLetter; standings: LeagueStanding[] }[]; knockout: LeagueTie[]; fixtures: LeagueFixture[]
  createdAt: string; completedAt: string | null
}
export interface LeagueSummary {
  id: string; ownerId: string; name: string; status: LeagueStatus; groupCount: 2 | 4; entrantCount: 8 | 16
  champion: { entrant: number; factionName: string } | null; progress: LeagueProgress; createdAt: string; completedAt: string | null
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

export async function listLeagues(signal?: AbortSignal): Promise<LeagueList> {
  const response = await leagueFetch('/v1/leagues', { signal })
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
