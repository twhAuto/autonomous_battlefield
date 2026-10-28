// Pure league presentation: labels, battle counts and the setup form's estimates.

import type { GroupLetter, League, LeagueFixture, LeagueStage } from '../api/leagues'

export const groupLetters: GroupLetter[] = ['A', 'B', 'C', 'D']

/** Fixtures a league plays when nothing is cancelled: group pairings twice, then two-leg ties. */
export function totalFixtures(entrants: 8 | 16) {
  return entrants === 8 ? 30 : 62
}

/** Measured decision waits per model window (API about 13 s, CLI about 35 s). */
const decisionSeconds = { low: 13, high: 35 }

export interface LeagueEstimate {
  battles: number
  decisionsPerModelSide: number
  /** Upper bound: every model side decides at every window of a full-length battle. */
  modelDecisions: number
  battleMinutes: { low: number; high: number }
  leagueHours: { low: number; high: number }
}

/** Estimates a league from its settings. modelEntrants counts entrants with a model controller. */
export function estimateLeague(entrants: 8 | 16, modelEntrants: number, ticks: number, tickSeconds: number, interval: number): LeagueEstimate {
  const battles = totalFixtures(entrants)
  const windows = Math.ceil(ticks / Math.max(1, interval))
  const modelSidesPerBattle = (2 * modelEntrants) / entrants
  const waits = modelEntrants > 0 ? decisionSeconds : { low: 0, high: 0 }
  const minutes = (wait: number) => (ticks * tickSeconds + windows * wait) / 60
  const battleMinutes = { low: minutes(waits.low), high: minutes(waits.high) }
  return {
    battles, decisionsPerModelSide: windows, modelDecisions: Math.round(battles * modelSidesPerBattle * windows), battleMinutes,
    leagueHours: { low: (battles * battleMinutes.low) / 60, high: (battles * battleMinutes.high) / 60 },
  }
}

/** Parses a Go duration such as 500ms, 1s or 1.5s; returns undefined for other forms. */
export function durationSeconds(value: string | undefined): number | undefined {
  const match = /^(\d+(?:\.\d+)?)(ms|s)$/.exec(value ?? '')
  if (!match) return undefined
  return Number(match[1]) / (match[2] === 'ms' ? 1000 : 1)
}

const stageNames: Record<LeagueStage, string> = { group: 'Group stage', quarterfinal: 'Quarterfinals', semifinal: 'Semifinals', final: 'Final' }
export function stageLabel(stage: LeagueStage) {
  return stageNames[stage]
}

export function fixtureLabel(fixture: LeagueFixture) {
  const place = fixture.stage === 'group' ? `Group ${fixture.group} · round ${fixture.round}` : `${stageLabel(fixture.stage)} · tie ${fixture.tie}`
  return `${place} · leg ${fixture.leg}`
}

export function entrantName(league: League, index: number) {
  return league.entrants[index]?.factionName ?? `Entrant ${index + 1}`
}

/** The fixture's outcome from its result, or its status while unsettled. */
export function fixtureOutcome(league: League, fixture: LeagueFixture) {
  if (!fixture.result) return fixture.status === 'void' ? 'Void' : fixture.status[0].toUpperCase() + fixture.status.slice(1)
  if (fixture.result.winner === null) return 'Draw'
  return `${entrantName(league, fixture.result.winner)} wins`
}
