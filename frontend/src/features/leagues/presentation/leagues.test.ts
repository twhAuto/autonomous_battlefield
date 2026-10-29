import { describe, expect, it } from 'vitest'
import type { League, LeagueFixture } from '../api/leagues'
import { durationSeconds, entrantName, estimateLeague, fixtureLabel, fixtureOutcome, totalFixtures } from './leagues'

const fixture = (change: Partial<LeagueFixture> = {}): LeagueFixture => ({
  id: 'f', sequence: 0, stage: 'group', group: 'A', round: 2, tie: 2, match: 'losers', homeEntrant: 0, awayEntrant: 1, scenarioId: 'frontier', status: 'pending', attempts: 0, battleId: null, winner: null, ...change,
})
const league = { entrants: [{ index: 0, factionName: 'Amber' }, { index: 1, factionName: 'Basalt' }] } as League

describe('league estimates', () => {
  const settings = (entrants: 8 | 16, modelEntrants: number, ticks: number, tickSeconds: number, interval: number, cliEntrants = 0) =>
    ({ entrants, modelEntrants, cliEntrants, ticks, tickSeconds, interval })

  it('counts battles, decisions and time', () => {
    expect(totalFixtures(8)).toBe(13)
    expect(totalFixtures(16)).toBe(27)
    // The opening decision plus one every 10 ticks through tick 500.
    const estimate = estimateLeague(settings(8, 4, 500, 1, 10))
    expect(estimate).toMatchObject({ battles: 13, decisionsPerModelSide: 51, battlesAtOnce: { low: 1, high: 1 } })
    expect(estimate.battleMinutes.low).toBeCloseTo((500 + 51 * 13) / 60)
    expect(estimate.battleMinutes.high).toBeCloseTo((500 + 51 * 35) / 60)
    // Without a reported capacity, battles run one at a time.
    expect(estimate.leagueHours.high).toBeCloseTo(13 * estimate.battleMinutes.high / 60)
    expect(estimateLeague(settings(8, 1, 495, 1, 10)).decisionsPerModelSide).toBe(50)
  })
  it('bounds model decisions by the most battles model entrants can play', () => {
    // One model entrant: three group battles through the decider, then the semifinal and final.
    expect(estimateLeague(settings(8, 1, 500, 1, 10)).modelDecisions).toBe(5 * 51)
    // Four of eight: twelve group battles, then all four semifinalists and both finalists.
    expect(estimateLeague(settings(8, 4, 500, 1, 10)).modelDecisions).toBe((12 + 4 + 2) * 51)
    // Every side of every battle is a model when all entrants are.
    expect(estimateLeague(settings(8, 8, 500, 1, 10)).modelDecisions).toBe(13 * 2 * 51)
    expect(estimateLeague(settings(16, 16, 500, 1, 10)).modelDecisions).toBe(27 * 2 * 51)
    expect(estimateLeague(settings(16, 3, 500, 1, 10)).modelDecisions).toBe((9 + 3 + 3 + 2) * 51)
  })
  it('runs each stage in rounds of battles that fit the slots', () => {
    const slots = { battleSlots: 4, cliSlots: 4 }
    // Eight scripted entrants: the groups' 4, 4 and 2 battles in a round each, then the semifinals and the final.
    const scripted = estimateLeague(settings(8, 0, 300, 1, 10), slots)
    expect(scripted.battlesAtOnce).toEqual({ low: 4, high: 4 })
    expect(scripted.leagueHours.low).toBeCloseTo(5 * scripted.battleMinutes.low / 60)
    // Sixteen: the groups' 8, 8 and 4 battles in 2, 2 and 1 rounds, then 1 each for the quarterfinals, semifinals and final.
    const large = estimateLeague(settings(16, 0, 300, 1, 10), slots)
    expect(large.leagueHours.low).toBeCloseTo(8 * large.battleMinutes.low / 60)
    // Battles with two CLI sides share four CLI slots two at a time: 2 + 2 + 1 + 1 + 1 rounds at worst.
    const cli = estimateLeague(settings(8, 4, 300, 1, 10, 4), slots)
    expect(cli.battlesAtOnce).toEqual({ low: 2, high: 4 })
    expect(cli.leagueHours.low).toBeCloseTo(5 * cli.battleMinutes.low / 60)
    expect(cli.leagueHours.high).toBeCloseTo(7 * cli.battleMinutes.high / 60)
    // A single CLI entrant has at most one CLI side per battle.
    expect(estimateLeague(settings(8, 1, 300, 1, 10, 1), slots).battlesAtOnce).toEqual({ low: 4, high: 4 })
    expect(estimateLeague(settings(8, 2, 300, 1, 10, 2), { battleSlots: 4, cliSlots: 1 }).battlesAtOnce).toEqual({ low: 1, high: 4 })
  })
  it('adds no decision waits for scripted leagues', () => {
    const estimate = estimateLeague(settings(16, 0, 300, 0.5, 0))
    expect(estimate.modelDecisions).toBe(0)
    expect(estimate.battleMinutes.low).toBe(estimate.battleMinutes.high)
    expect(estimate.decisionsPerModelSide).toBe(301)
  })
  it('parses Go durations', () => {
    expect(durationSeconds('500ms')).toBe(0.5)
    expect(durationSeconds('1.5s')).toBe(1.5)
    expect(durationSeconds('1m')).toBeUndefined()
    expect(durationSeconds(undefined)).toBeUndefined()
  })
})

describe('fixture labels', () => {
  it('names matches and outcomes, with the tiebreak winner after a draw or a void fixture', () => {
    expect(fixtureLabel(fixture())).toBe("Group A · Losers' match")
    expect(fixtureLabel(fixture({ stage: 'final', group: undefined, match: undefined, tie: 1 }))).toBe('Final · tie 1')
    expect(fixtureOutcome(league, fixture())).toBe('Pending')
    expect(fixtureOutcome(league, fixture({ status: 'void' }))).toBe('Void')
    expect(fixtureOutcome(league, fixture({ status: 'void', winner: 0 }))).toBe('Void · Amber wins on tiebreak')
    expect(fixtureOutcome(league, fixture({ status: 'completed', result: { winner: null, reason: 'tick_limit' }, winner: 1 }))).toBe('Draw · Basalt wins on tiebreak')
    expect(fixtureOutcome(league, fixture({ status: 'completed', result: { winner: null, reason: 'tick_limit' } }))).toBe('Draw')
    expect(fixtureOutcome(league, fixture({ status: 'completed', result: { winner: 1, reason: 'castle_destroyed' }, winner: 1 }))).toBe('Basalt wins')
    expect(entrantName(league, 5)).toBe('Entrant 6')
  })
})
