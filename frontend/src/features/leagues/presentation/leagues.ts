// Pure league presentation: labels, battle counts and the setup form's estimates.

import type { GroupLetter, League, LeagueFixture, LeagueScheduling, LeagueStage } from '../api/leagues'

export const groupLetters: GroupLetter[] = ['A', 'B', 'C', 'D']

/** Fixtures a league plays when nothing is cancelled: group pairings twice, then two-leg ties. */
export function totalFixtures(entrants: 8 | 16) {
  return entrants === 8 ? 30 : 62
}

/** Measured decision waits per model window (API about 13 s, CLI about 35 s). */
const decisionSeconds = { low: 13, high: 35 }

// Each entrant plays every group rival twice (six battles); each knockout stage holds
// this many entrants, each playing two legs.
const groupBattlesPerEntrant = 6
const knockoutStageEntrants = { 8: [4, 2], 16: [8, 4, 2] }

/** Assumed when the API does not report its capacity: one battle at a time. */
const oneAtATime: LeagueScheduling = { battleSlots: 1, cliSlots: 1 }

export interface LeagueSettingsDraft {
  entrants: 8 | 16
  /** Entrants with a model controller, and those whose model runs as a CLI process. */
  modelEntrants: number
  cliEntrants: number
  ticks: number
  tickSeconds: number
  interval: number
}

export interface LeagueEstimate {
  battles: number
  /** Battles that run at once: all battle slots, down to what the CLI slots allow when every battle has two CLI sides. */
  battlesAtOnce: { low: number; high: number }
  /** The opening decision plus one at every interval that a full-length battle reaches. */
  decisionsPerModelSide: number
  /** Upper bound: model entrants reach every knockout stage and every battle runs its full length. */
  modelDecisions: number
  battleMinutes: { low: number; high: number }
  leagueHours: { low: number; high: number }
}

/** Battles model entrants can play at most: all group battles, plus two legs per knockout stage they could reach. */
function modelSideBattles(entrants: 8 | 16, modelEntrants: number) {
  return modelEntrants * groupBattlesPerEntrant + knockoutStageEntrants[entrants].reduce((sum, stage) => sum + 2 * Math.min(modelEntrants, stage), 0)
}

/** Rounds of battles a league needs when this many run at once; each stage waits for the one before. */
function waves(entrants: 8 | 16, atOnce: number) {
  const stages = [entrants * 3, ...knockoutStageEntrants[entrants]]
  return stages.reduce((sum, fixtures) => sum + Math.ceil(fixtures / atOnce), 0)
}

/** Estimates a league from its settings and the capacity its battles run in. */
export function estimateLeague(settings: LeagueSettingsDraft, scheduling: LeagueScheduling = oneAtATime): LeagueEstimate {
  const { entrants, modelEntrants, cliEntrants, ticks, tickSeconds, interval } = settings
  const battles = totalFixtures(entrants)
  const windows = Math.floor(ticks / Math.max(1, interval)) + 1
  const waits = modelEntrants > 0 ? decisionSeconds : { low: 0, high: 0 }
  const minutes = (wait: number) => (ticks * tickSeconds + windows * wait) / 60
  const battleMinutes = { low: minutes(waits.low), high: minutes(waits.high) }
  const cliPerBattle = Math.min(2, cliEntrants)
  const fewest = cliPerBattle > 0 ? Math.max(1, Math.min(scheduling.battleSlots, Math.floor(scheduling.cliSlots / cliPerBattle))) : scheduling.battleSlots
  return {
    battles, battlesAtOnce: { low: fewest, high: scheduling.battleSlots }, decisionsPerModelSide: windows,
    modelDecisions: modelSideBattles(entrants, modelEntrants) * windows, battleMinutes,
    leagueHours: { low: (waves(entrants, scheduling.battleSlots) * battleMinutes.low) / 60, high: (waves(entrants, fewest) * battleMinutes.high) / 60 },
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
