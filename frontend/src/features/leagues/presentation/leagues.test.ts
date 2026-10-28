import { describe, expect, it } from 'vitest'
import type { League, LeagueFixture } from '../api/leagues'
import { durationSeconds, entrantName, estimateLeague, fixtureLabel, fixtureOutcome, stageLabel, totalFixtures } from './leagues'

const fixture = (change: Partial<LeagueFixture> = {}): LeagueFixture => ({
  id: 'f', sequence: 0, stage: 'group', group: 'A', round: 2, leg: 1, homeEntrant: 0, awayEntrant: 1, scenarioId: 'frontier', status: 'pending', attempts: 0, battleId: null, ...change,
})
const league = { entrants: [{ index: 0, factionName: 'Amber' }, { index: 1, factionName: 'Basalt' }] } as League

describe('league estimates', () => {
  it('counts battles, decisions and time', () => {
    expect(totalFixtures(8)).toBe(30)
    expect(totalFixtures(16)).toBe(62)
    // 4 of 8 entrants are models: one model side per battle on average.
    const estimate = estimateLeague(8, 4, 500, 1, 10)
    expect(estimate).toMatchObject({ battles: 30, decisionsPerModelSide: 50, modelDecisions: 1500 })
    expect(estimate.battleMinutes.low).toBeCloseTo((500 + 50 * 13) / 60)
    expect(estimate.battleMinutes.high).toBeCloseTo((500 + 50 * 35) / 60)
    expect(estimate.leagueHours.high).toBeCloseTo(30 * estimate.battleMinutes.high / 60)
  })
  it('adds no decision waits for scripted leagues', () => {
    const estimate = estimateLeague(16, 0, 300, 0.5, 0)
    expect(estimate.modelDecisions).toBe(0)
    expect(estimate.battleMinutes.low).toBe(estimate.battleMinutes.high)
    expect(estimate.decisionsPerModelSide).toBe(300)
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
