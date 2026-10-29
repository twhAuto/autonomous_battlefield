// Group tables, knockout bracket and fixture list for one league. Entrants are named
// by faction with their controller; the top two of each group are marked as qualifying.

import { Link } from 'react-router-dom'
import type { League, LeagueStage, LeagueTie } from '../api/leagues'
import { controllerName } from '../../../shared/domain/controllers'
import { entrantName, fixtureLabel, fixtureOutcome, stageLabel } from '../presentation/leagues'

function Entrant({ league, index }: Readonly<{ league: League; index: number }>) {
  const entrant = league.entrants[index]
  return <><strong>{entrantName(league, index)}</strong>{entrant && <small>{controllerName(entrant.controller)}</small>}</>
}

function signed(value: number) {
  return value > 0 ? `+${value}` : String(value)
}

export function GroupTables({ league }: Readonly<{ league: League }>) {
  return (
    <section className="league-groups" aria-label="Group tables">
      {league.groups.map(group => <div key={group.group} className="league-group">
        <div className="section-label"><span>GROUP {group.group}</span><span>TOP TWO QUALIFY</span></div>
        <table className="league-table">
          <caption className="visually-hidden">Group {group.group} standings</caption>
          <thead><tr><th scope="col">#</th><th scope="col">Entrant</th><th scope="col"><abbr title="Played">P</abbr></th><th scope="col"><abbr title="Won">W</abbr></th>
            <th scope="col"><abbr title="Drawn">D</abbr></th><th scope="col"><abbr title="Lost">L</abbr></th><th scope="col"><abbr title="Points">Pts</abbr></th>
            <th scope="col"><abbr title="Castle-health difference">Castle ±</abbr></th><th scope="col"><abbr title="Damage difference">Damage ±</abbr></th></tr></thead>
          <tbody>{group.standings.map(standing => <tr key={standing.entrant} className={standing.position <= 2 ? 'league-table__qualifier' : undefined}>
            <td>{standing.position}</td>
            <th scope="row"><Entrant league={league} index={standing.entrant} /></th>
            <td>{standing.played}</td><td>{standing.won}</td><td>{standing.drawn}</td><td>{standing.lost}</td>
            <td><strong>{standing.points}</strong></td><td>{signed(standing.castleDifference)}</td><td>{signed(standing.damageDifference)}</td>
          </tr>)}</tbody>
        </table>
      </div>)}
    </section>
  )
}

function bracketStages(groupCount: 2 | 4): LeagueStage[] {
  return groupCount === 4 ? ['quarterfinal', 'semifinal', 'final'] : ['semifinal', 'final']
}

function TieCard({ league, tie }: Readonly<{ league: League; tie: LeagueTie }>) {
  const side = (index: number) => <li className={tie.winner === index ? 'league-tie__winner' : undefined}><Entrant league={league} index={index} />{tie.winner === index && <span className="visually-hidden"> (winner)</span>}</li>
  return <ol className="league-tie" aria-label={`${stageLabel(tie.stage)} tie ${tie.tie}`}>{side(tie.homeEntrant)}{side(tie.awayEntrant)}</ol>
}

export function KnockoutBracket({ league }: Readonly<{ league: League }>) {
  const stages = bracketStages(league.groupCount)
  return (
    <section className="league-bracket" aria-label="Knockout bracket">
      {stages.map((stage, column) => {
        const ties = league.knockout.filter(tie => tie.stage === stage)
        const expected = 2 ** (stages.length - column - 1)
        return <div key={stage} className="league-bracket__stage">
          <div className="section-label"><span>{stageLabel(stage).toUpperCase()}</span><span>TWO LEGS</span></div>
          {ties.map(tie => <TieCard key={tie.tie} league={league} tie={tie} />)}
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
        <thead><tr><th>#</th><th>Fixture</th><th>Home</th><th>Away</th><th>Map</th><th>Result</th><th><span className="visually-hidden">Battle</span></th></tr></thead>
        <tbody>{league.fixtures.map(fixture => <tr key={fixture.id}>
          <td data-label="#">{fixture.sequence + 1}</td>
          <td data-label="Fixture">{fixtureLabel(fixture)}{fixture.attempts > 1 && <small>Retried</small>}</td>
          <td data-label="Home"><Entrant league={league} index={fixture.homeEntrant} /></td>
          <td data-label="Away"><Entrant league={league} index={fixture.awayEntrant} /></td>
          <td data-label="Map">{fixture.scenarioId}</td>
          <td data-label="Result"><span className="history-status">{fixtureOutcome(league, fixture)}</span></td>
          <td>{fixture.battleId && <Link className="text-link" to={`/battles/${encodeURIComponent(fixture.battleId)}`}>Open battle →</Link>}</td>
        </tr>)}</tbody>
      </table>
    </section>
  )
}
