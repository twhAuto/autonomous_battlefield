// Names a league fixture for people: a group's opening, winners' or losers' match or its
// decider, or a knockout stage's tie. The league and battle pages share these names.

export type LeagueMatch = 'opening' | 'winners' | 'losers' | 'decider'

/** Where a fixture sits in its league, as league and battle reads report it. */
export interface MatchPlace { stage: string; group?: string; tie: number; match?: LeagueMatch }

const stageNames: Record<string, string> = { group: 'Group stage', quarterfinal: 'Quarterfinals', semifinal: 'Semifinals', final: 'Final' }
const groupMatchNames: Record<Exclude<LeagueMatch, 'opening'>, string> = { winners: "Winners' match", losers: "Losers' match", decider: 'Decider' }

export function stageLabel(stage: string) {
  return stageNames[stage] ?? stage
}

/** A group match's name; the two opening matches are numbered by tie. */
export function groupMatchName(match: LeagueMatch | undefined, tie: number) {
  return !match || match === 'opening' ? `Opening match ${tie}` : groupMatchNames[match]
}

export function matchLabel(place: MatchPlace) {
  if (place.stage !== 'group') return `${stageLabel(place.stage)} · tie ${place.tie}`
  return `Group ${place.group} · ${groupMatchName(place.match, place.tie)}`
}
