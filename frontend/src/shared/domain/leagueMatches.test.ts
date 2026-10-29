import { expect, it } from 'vitest'
import { groupMatchName, matchLabel, stageLabel } from './leagueMatches'

it('names group matches by their place in the dual tournament', () => {
  expect(matchLabel({ stage: 'group', group: 'A', tie: 2, match: 'opening' })).toBe('Group A · Opening match 2')
  expect(matchLabel({ stage: 'group', group: 'B', tie: 1, match: 'winners' })).toBe("Group B · Winners' match")
  expect(groupMatchName('losers', 2)).toBe("Losers' match")
  expect(groupMatchName('decider', 1)).toBe('Decider')
})

it('names knockout ties by stage', () => {
  expect(matchLabel({ stage: 'semifinal', tie: 2 })).toBe('Semifinals · tie 2')
  expect(stageLabel('quarterfinal')).toBe('Quarterfinals')
  expect(stageLabel('unknown')).toBe('unknown')
})
