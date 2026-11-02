// Analytics report payloads and the request that builds one from completed battles.

import { createApiClient } from '../../../shared/api/request'
export { controllerName } from '../../../shared/domain/controllers'

export interface AnalyticsController { type: 'scripted' | 'model'; profileId?: string; provider?: string; runtime?: string; modelIdentifier?: string; reasoningEffort?: string }
export interface AnalyticsOutcome {
  battles: number; wins: number; draws: number; losses: number; castleWins: number
  winRate: number; winRateLow: number; winRateHigh: number; meanTicksToWin: number | null
}
export interface ModelStats extends AnalyticsOutcome {
  controller: AnalyticsController; decisions: number; fallbackRate: number; invalidPlanRate: number
  meanDecisionLatencyMs: number | null; tokensPerBattle: { input: number; cachedInput: number; output: number }
}
export interface FactionStats extends AnalyticsOutcome { factionId: string; factionName: string }
export interface Matchup { controller: AnalyticsController; opponent: AnalyticsController; battles: number; wins: number; draws: number; losses: number }
export interface UnitStats {
  unitType: string; fielded: number; recruited: number; lost: number; kills: number; buildingsDestroyed: number
  damageDealt: number; damageTaken: number; healthRestored: number; goldSpent: number
  killsPerLoss: number | null; damagePerGold: number | null; shareInWins: number; shareInLosses: number
}
export interface ModelUnit { controller: AnalyticsController; unitType: string; recruited: number; armyShare: number }
export interface UpgradeStats { upgradeId: string; purchases: number; purchaseRate: number; meanPurchaseTick: number; winRateWhenPurchased: number | null; winRateWhenNot: number | null }
export interface MapStats { scenarioId: string; battles: number; draws: number; drawRate: number; meanTicks: number; sides: [{ wins: number; winRate: number }, { wins: number; winRate: number }] }
export interface AnalyticsReport {
  rulesVersion: string | null; truncated: boolean; excludedForFallback: number; battles: number; battlesWithFallback: number; sidesWithUnitStats: number
  models: ModelStats[]; factions: FactionStats[]; headToHead: Matchup[]; units: UnitStats[]; unitsByModel: ModelUnit[]; upgrades: UpgradeStats[]; maps: MapStats[]
}
export interface AnalyticsQuery { leagueId?: string; allRules?: boolean; excludeFallback?: boolean }

export class AnalyticsApiError extends Error {
  constructor(message: string, public readonly status?: number) {
    super(message)
    this.name = 'AnalyticsApiError'
  }
}

const analyticsFetch = createApiClient(AnalyticsApiError)

export async function getAnalytics(query: AnalyticsQuery, signal?: AbortSignal): Promise<AnalyticsReport> {
  const params = new URLSearchParams()
  if (query.leagueId) params.set('leagueId', query.leagueId)
  if (query.allRules) params.set('rulesVersion', 'all')
  if (query.excludeFallback) params.set('excludeFallback', 'true')
  const suffix = params.size ? `?${params}` : ''
  const response = await analyticsFetch(`/v1/analytics${suffix}`, { signal })
  if (response.ok) return (await response.json()) as AnalyticsReport
  if (response.status === 403) throw new AnalyticsApiError('This account cannot view statistics.', 403)
  throw new AnalyticsApiError('Statistics could not be loaded.', response.status)
}

export function controllerKey(controller: AnalyticsController) {
  return [controller.type, controller.profileId, controller.modelIdentifier, controller.reasoningEffort, controller.provider, controller.runtime].join('|')
}
