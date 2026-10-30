// Pure league presentation: labels, battle counts and the setup form's estimates.

import type { GroupLetter, League, LeagueFixture, LeagueScheduling } from '../api/leagues'
import { matchLabel } from '../../../shared/domain/leagueMatches'

export const groupLetters: GroupLetter[] = ['A', 'B', 'C', 'D']

/** Fixtures a league plays when nothing is cancelled: five per group, then one per knockout tie. */
export function totalFixtures(entrants: 8 | 16) {
  return entrants === 8 ? 13 : 27
}

/** Measured decision waits per model window (API about 13 s, CLI about 35 s). */
const decisionSeconds = { low: 13, high: 35 }

// A group's five matches give its entrants ten sides: two or three battles each, three
// for the two who reach the decider. Each knockout stage holds this many entrants, one
// battle each.
const groupBattlesAtMost = 3
const groupSidesPerEntrant = 2.5
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

/** Battles model entrants can play at most: three group battles each, up to every group side, plus each knockout stage they could reach. */
function modelSideBattles(entrants: 8 | 16, modelEntrants: number) {
  const group = Math.min(modelEntrants * groupBattlesAtMost, entrants * groupSidesPerEntrant)
  return group + knockoutStageEntrants[entrants].reduce((sum, stage) => sum + Math.min(modelEntrants, stage), 0)
}

/** Rounds of battles a league needs when this many run at once: the groups' three rounds, then each knockout stage, each waiting for the one before. */
function waves(entrants: 8 | 16, atOnce: number) {
  const rounds = [entrants / 2, entrants / 2, entrants / 4, ...knockoutStageEntrants[entrants].map(stage => stage / 2)]
  return rounds.reduce((sum, fixtures) => sum + Math.ceil(fixtures / atOnce), 0)
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

export function fixtureLabel(fixture: LeagueFixture) {
  return matchLabel(fixture)
}

export function entrantName(league: League, index: number) {
  return league.entrants[index]?.factionName ?? `Entrant ${index + 1}`
}

/** A forfeited fixture's outcome: a walkover without a battle, a forfeit of one that started, or the seed's walkover when both sides were disqualified. */
function forfeitOutcome(league: League, fixture: LeagueFixture) {
  if (fixture.winner === null) return 'Forfeited'
  const winner = entrantName(league, fixture.winner)
  if (fixture.forfeit === 'both') return `Walkover · ${winner} advances on seed`
  return `${fixture.battleId ? 'Forfeit' : 'Walkover'} · ${winner} wins`
}

/** The fixture's outcome: its battle's winner, the tiebreak winner after a draw or a void fixture, a forfeit, or its status while undecided. */
export function fixtureOutcome(league: League, fixture: LeagueFixture) {
  const { result, winner } = fixture
  if (fixture.status === 'forfeited') return forfeitOutcome(league, fixture)
  if (result && result.winner !== null) return `${entrantName(league, result.winner)} wins`
  if (!result && fixture.status !== 'void') return fixture.status[0].toUpperCase() + fixture.status.slice(1)
  const decided = result ? 'Draw' : 'Void'
  return winner === null ? decided : `${decided} · ${entrantName(league, winner)} wins on tiebreak`
}
