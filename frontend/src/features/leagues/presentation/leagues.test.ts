import { describe, expect, it } from 'vitest'
import type { League, LeagueFixture } from '../api/leagues'
import { durationSeconds, entrantName, estimateLeague, fixtureLabel, fixtureOutcome, stageLabel, totalFixtures } from './leagues'

const fixture = (change: Partial<LeagueFixture> = {}): LeagueFixture => ({
  id: 'f', sequence: 0, stage: 'group', group: 'A', round: 2, leg: 1, homeEntrant: 0, awayEntrant: 1, scenarioId: 'frontier', status: 'pending', attempts: 0, battleId: null, ...change,
})
const league = { entrants: [{ index: 0, factionName: 'Amber' }, { index: 1, factionName: 'Basalt' }] } as League

describe('league estimates', () => {
  const settings = (entrants: 8 | 16, modelEntrants: number, ticks: number, tickSeconds: number, interval: number, cliEntrants = 0) =>
    ({ entrants, modelEntrants, cliEntrants, ticks, tickSeconds, interval })

  it('counts battles, decisions and time', () => {
    expect(totalFixtures(8)).toBe(30)
    expect(totalFixtures(16)).toBe(62)
    // The opening decision plus one every 10 ticks through tick 500.
    const estimate = estimateLeague(settings(8, 4, 500, 1, 10))
    expect(estimate).toMatchObject({ battles: 30, decisionsPerModelSide: 51, battlesAtOnce: { low: 1, high: 1 } })
    expect(estimate.battleMinutes.low).toBeCloseTo((500 + 51 * 13) / 60)
    expect(estimate.battleMinutes.high).toBeCloseTo((500 + 51 * 35) / 60)
    // Without a reported capacity, battles run one at a time.
    expect(estimate.leagueHours.high).toBeCloseTo(30 * estimate.battleMinutes.high / 60)
    expect(estimateLeague(settings(8, 1, 495, 1, 10)).decisionsPerModelSide).toBe(50)
  })
  it('bounds model decisions by the most battles model entrants can play', () => {
    // One model entrant: six group battles, then both legs of the semifinal and final.
    expect(estimateLeague(settings(8, 1, 500, 1, 10)).modelDecisions).toBe(10 * 51)
    // Four of eight: 24 group battles, then all four semifinalists and both finalists.
    expect(estimateLeague(settings(8, 4, 500, 1, 10)).modelDecisions).toBe((24 + 8 + 4) * 51)
    // Every side of every battle is a model when all entrants are.
    expect(estimateLeague(settings(8, 8, 500, 1, 10)).modelDecisions).toBe(30 * 2 * 51)
    expect(estimateLeague(settings(16, 16, 500, 1, 10)).modelDecisions).toBe(62 * 2 * 51)
    expect(estimateLeague(settings(16, 3, 500, 1, 10)).modelDecisions).toBe((18 + 6 + 6 + 4) * 51)
  })
  it('runs each stage in rounds of battles that fit the slots', () => {
    const slots = { battleSlots: 4, cliSlots: 4 }
    // Eight scripted entrants: 24 group battles in 6 rounds, the semifinals in 1, the final in 1.
    const scripted = estimateLeague(settings(8, 0, 300, 1, 10), slots)
    expect(scripted.battlesAtOnce).toEqual({ low: 4, high: 4 })
    expect(scripted.leagueHours.low).toBeCloseTo(8 * scripted.battleMinutes.low / 60)
    // Sixteen: 48 group battles in 12 rounds, then 2, 1 and 1.
    const large = estimateLeague(settings(16, 0, 300, 1, 10), slots)
    expect(large.leagueHours.low).toBeCloseTo(16 * large.battleMinutes.low / 60)
    // Battles with two CLI sides share four CLI slots two at a time: 12 + 2 + 1 rounds at worst.
    const cli = estimateLeague(settings(8, 4, 300, 1, 10, 4), slots)
    expect(cli.battlesAtOnce).toEqual({ low: 2, high: 4 })
    expect(cli.leagueHours.low).toBeCloseTo(8 * cli.battleMinutes.low / 60)
    expect(cli.leagueHours.high).toBeCloseTo(15 * cli.battleMinutes.high / 60)
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
  it('names stages, places and outcomes', () => {
    expect(stageLabel('quarterfinal')).toBe('Quarterfinals')
    expect(fixtureLabel(fixture())).toBe('Group A · round 2 · leg 1')
    expect(fixtureLabel(fixture({ stage: 'final', group: undefined, tie: 1, leg: 2 }))).toBe('Final · tie 1 · leg 2')
    expect(fixtureOutcome(league, fixture())).toBe('Pending')
    expect(fixtureOutcome(league, fixture({ status: 'void' }))).toBe('Void')
    expect(fixtureOutcome(league, fixture({ status: 'completed', result: { winner: null, reason: 'tick_limit' } }))).toBe('Draw')
    expect(fixtureOutcome(league, fixture({ status: 'completed', result: { winner: 1, reason: 'castle_destroyed' } }))).toBe('Basalt wins')
    expect(entrantName(league, 5)).toBe('Entrant 6')
  })
})
