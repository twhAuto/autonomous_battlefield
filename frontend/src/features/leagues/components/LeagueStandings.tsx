// Running battles, disqualifications, each group's dual tournament, knockout bracket and
// fixture list for one league. Entrants are named by faction with their controller and
// marked DQ once disqualified; the top two of each group are marked as qualifying once
// placed, and running fixtures show their battle's tick progress. Admins disqualify an
// entrant from its group table.

import { Link } from 'react-router-dom'
import type { GroupLetter, League, LeagueFixture, LeagueStage, LeagueStanding } from '../api/leagues'
import { controllerName } from '../../../shared/domain/controllers'
import { groupMatchName, stageLabel, type LeagueMatch } from '../../../shared/domain/leagueMatches'
import { entrantName, fixtureLabel, fixtureOutcome } from '../presentation/leagues'
import { DisqualifyEntrant } from './DisqualifyEntrant'

function Entrant({ league, index }: Readonly<{ league: League; index: number }>) {
  const entrant = league.entrants[index]
  return <>
    <strong>{entrantName(league, index)}</strong>
    {entrant?.disqualification && <span className="league-dq" title="Disqualified"><span aria-hidden="true">DQ</span><span className="visually-hidden"> (disqualified)</span></span>}
    {entrant && <small>{controllerName(entrant.controller)}</small>}
  </>
}

// Who was disqualified, by whom and when, with any reason given. Hidden until someone is.
export function Disqualifications({ league }: Readonly<{ league: League }>) {
  const removed = league.entrants.flatMap(({ index, disqualification }) => disqualification ? [{ index, ...disqualification }] : [])
  if (removed.length === 0) return null
  return (
    <section className="league-disqualifications" aria-label="Disqualifications">
      <div className="section-label"><span>DISQUALIFIED</span><span>{removed.length}</span></div>
      <ul>{removed.map(({ index, by, at, reason }) => <li key={index}>
        <strong>{entrantName(league, index)}</strong> by {by} · <time dateTime={at}>{new Date(at).toLocaleString()}</time>{reason && <> · {reason}</>}
      </li>)}</ul>
    </section>
  )
}

function BattleProgress({ fixture }: Readonly<{ fixture: LeagueFixture }>) {
  const progress = fixture.battleProgress
  if (!progress) return <small>Starting…</small>
  return <span className="league-battle-progress">
    <progress className="battle-progress" max={progress.requestedTicks} value={progress.currentTick} aria-label={`${fixtureLabel(fixture)} ticks played`} />
    <small>Tick {progress.currentTick} of {progress.requestedTicks}</small>
  </span>
}

// Every running fixture first, so a viewer finds the live battles without scanning the
// fixture list. Hidden while nothing runs.
export function NowPlaying({ league }: Readonly<{ league: League }>) {
  const running = league.fixtures.filter(fixture => fixture.status === 'running')
  if (running.length === 0) return null
  return (
    <section className="league-live" aria-label="Now playing">
      <div className="section-label"><span><span className="status-dot" aria-hidden="true" /> NOW PLAYING</span><span>{running.length} RUNNING</span></div>
      <ul className="league-live__battles">{running.map(fixture => <li key={fixture.id} className="league-live__battle">
        <span className="league-live__fixture">{fixtureLabel(fixture)} · {fixture.scenarioId}</span>
        <div className="league-live__sides">
          <div><Entrant league={league} index={fixture.homeEntrant} /></div>
          <span aria-hidden="true">VS</span><span className="visually-hidden">versus</span>
          <div><Entrant league={league} index={fixture.awayEntrant} /></div>
        </div>
        <BattleProgress fixture={fixture} />
        {fixture.battleId && <Link className="compact-action" to={`/battles/${encodeURIComponent(fixture.battleId)}`}>WATCH LIVE →</Link>}
      </li>)}</ul>
    </section>
  )
}

const placements = ['1st', '2nd', '3rd', '4th']

// A group's dual tournament in play order: two openings, the winners' and losers'
// matches, then the decider.
const groupMatches: { round: number; tie: number; match: LeagueMatch }[] = [
  { round: 1, tie: 1, match: 'opening' }, { round: 1, tie: 2, match: 'opening' },
  { round: 2, tie: 1, match: 'winners' }, { round: 2, tie: 2, match: 'losers' }, { round: 3, tie: 1, match: 'decider' },
]

/** How an Admin disqualifies a running league's entrants: one confirmation at a time. */
export interface EntrantManagement {
  confirming: number | null
  start: (entrant: number) => void
  close: () => void
  done: (league: League) => void
}

function standingClass(league: League, standing: LeagueStanding) {
  const classes = [
    standing.placement !== null && standing.placement <= 2 && 'league-table__qualifier',
    league.entrants[standing.entrant]?.disqualification && 'league-table__disqualified',
  ].filter(Boolean)
  return classes.length > 0 ? classes.join(' ') : undefined
}

// An entrant can be disqualified while it can still win and has not been already.
function canDisqualify(league: League, index: number) {
  const entrant = league.entrants[index]
  return entrant !== undefined && !entrant.eliminated && !entrant.disqualification
}

export function GroupStage({ league, manage }: Readonly<{ league: League; manage?: EntrantManagement }>) {
  const confirming = manage?.confirming ?? null
  return (
    <section className="league-groups" aria-label="Group stage">
      {league.groups.map(group => <div key={group.group} className="league-group">
        <div className="section-label"><span>GROUP {group.group}</span><span>TOP TWO QUALIFY</span></div>
        <table className="league-table">
          <caption className="visually-hidden">Group {group.group} standings</caption>
          <thead><tr><th scope="col"><abbr title="Placement">#</abbr></th><th scope="col">Entrant</th><th scope="col"><abbr title="Won">W</abbr></th><th scope="col"><abbr title="Lost">L</abbr></th>
            {manage && <th scope="col"><span className="visually-hidden">Actions</span></th>}</tr></thead>
          <tbody>{group.standings.map(standing => <tr key={standing.entrant} className={standingClass(league, standing)}>
            <td>{standing.placement === null ? '–' : placements[standing.placement - 1]}</td>
            <th scope="row"><Entrant league={league} index={standing.entrant} /></th>
            <td>{standing.won}</td><td>{standing.lost}</td>
            {manage && <td>{canDisqualify(league, standing.entrant) && <button className="compact-action compact-action--danger league-table__action" type="button"
              aria-label={`Disqualify ${entrantName(league, standing.entrant)}`} onClick={() => manage.start(standing.entrant)}>DISQUALIFY</button>}</td>}
          </tr>)}</tbody>
        </table>
        {manage && confirming !== null && league.entrants[confirming]?.group === group.group &&
          <DisqualifyEntrant key={confirming} league={league} entrant={confirming} onDone={manage.done} onClose={manage.close} />}
        <GroupMatches league={league} group={group.group} />
      </div>)}
    </section>
  )
}

function GroupMatches({ league, group }: Readonly<{ league: League; group: GroupLetter }>) {
  return <ol className="league-group__matches" aria-label={`Group ${group} matches`}>
    {groupMatches.map(({ round, tie, match }) => {
      const fixture = league.fixtures.find(f => f.stage === 'group' && f.group === group && f.round === round && f.tie === tie)
      const name = groupMatchName(match, tie)
      return <li key={name}>
        <span className="league-match__name">{name}</span>
        {fixture ? <MatchCard league={league} label={`Group ${group} ${name}`} home={fixture.homeEntrant} away={fixture.awayEntrant} winner={fixture.winner} />
          : <p className="league-tie league-tie--pending">To be decided</p>}
      </li>
    })}
  </ol>
}

function bracketStages(groupCount: 2 | 4): LeagueStage[] {
  return groupCount === 4 ? ['quarterfinal', 'semifinal', 'final'] : ['semifinal', 'final']
}

// One match, side 1 (the first spawn) first, with its winner marked once decided.
function MatchCard({ league, label, home, away, winner }: Readonly<{ league: League; label: string; home: number; away: number; winner: number | null }>) {
  const side = (index: number) => <li className={winner === index ? 'league-tie__winner' : undefined}><Entrant league={league} index={index} />{winner === index && <span className="visually-hidden"> (winner)</span>}</li>
  return <ol className="league-tie" aria-label={label}>{side(home)}{side(away)}</ol>
}

export function KnockoutBracket({ league }: Readonly<{ league: League }>) {
  const stages = bracketStages(league.groupCount)
  return (
    <section className="league-bracket" aria-label="Knockout bracket">
      {stages.map((stage, column) => {
        const ties = league.knockout.filter(tie => tie.stage === stage)
        const expected = 2 ** (stages.length - column - 1)
        return <div key={stage} className="league-bracket__stage">
          <div className="section-label"><span>{stageLabel(stage).toUpperCase()}</span><span>ONE BATTLE</span></div>
          {ties.map(tie => <MatchCard key={tie.tie} league={league} label={`${stageLabel(tie.stage)} tie ${tie.tie}`} home={tie.homeEntrant} away={tie.awayEntrant} winner={tie.winner} />)}
          {Array.from({ length: expected - ties.length }, (_, index) => <p key={index} className="league-tie league-tie--pending">To be decided</p>)}
        </div>
      })}
    </section>
  )
}

export function FixtureList({ league }: Readonly<{ league: League }>) {
  return (
    <section aria-label="Fixtures">
      <div className="section-label"><span>FIXTURES</span><span>{league.progress.settled} / {league.progress.total} SETTLED</span></div>
      <table className="history-table league-fixtures">
        <caption className="visually-hidden">Fixtures in play order</caption>
        <thead><tr><th>#</th><th>Fixture</th><th>Side 1</th><th>Side 2</th><th>Map</th><th>Result</th><th><span className="visually-hidden">Battle</span></th></tr></thead>
        <tbody>{league.fixtures.map(fixture => <tr key={fixture.id} className={fixture.status === 'running' ? 'league-fixtures__running' : undefined}>
          <td data-label="#">{fixture.sequence + 1}</td>
          <td data-label="Fixture">{fixtureLabel(fixture)}{fixture.attempts > 1 && <small>Retried</small>}</td>
          <td data-label="Side 1"><Entrant league={league} index={fixture.homeEntrant} /></td>
          <td data-label="Side 2"><Entrant league={league} index={fixture.awayEntrant} /></td>
          <td data-label="Map">{fixture.scenarioId}</td>
          <td data-label="Result"><span className="history-status">{fixtureOutcome(league, fixture)}</span>{fixture.status === 'running' && <BattleProgress fixture={fixture} />}</td>
          <td>{fixture.battleId && <Link className="text-link" to={`/battles/${encodeURIComponent(fixture.battleId)}`}>Open battle →</Link>}</td>
        </tr>)}</tbody>
      </table>
    </section>
  )
}
