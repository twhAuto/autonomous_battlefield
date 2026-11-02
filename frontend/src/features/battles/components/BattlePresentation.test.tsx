import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, it } from 'vitest'
import type { BattleRun, FactionStatistics } from '../api/battles'
import { BattleHeader, BattleStatistics, Outcome } from './BattlePresentation'

const battle = { id: 'b', scenarioId: 'frontier-siege', attempt: 1, status: 'running' } as BattleRun

function header(run: BattleRun) {
  return render(<MemoryRouter><BattleHeader battle={run} battleTitle="Battle in progress" connection="live" connectionMessage="" retryConnection={() => undefined} /></MemoryRouter>)
}

const factionStats = (factionId: string) => ({ factionId, castleHealth: 0, unitsStarted: 1, unitsRemaining: 1, damageDealt: 0, damageTaken: 0, attackTicks: 0, defendTicks: 0 }) as FactionStatistics
const completed = {
  ...battle, status: 'completed',
  participants: [
    { id: 'jov', name: 'Jov', controller: { type: 'model', modelIdentifier: 'gpt-6.1-sol', reasoningEffort: 'high' } },
    { id: 'ws', name: 'WS', controller: { type: 'scripted' } },
  ],
  result: { winnerFactionId: 'jov', reason: 'castle_destroyed', ticksRun: 108, factions: [factionStats('jov'), factionStats('ws')] },
} as BattleRun
const names = new Map([['jov', 'Jov'], ['ws', 'WS']])

it('links a league battle to its league and fixture', () => {
  header({ ...battle, league: { id: 'league-1', name: 'Autumn cup', fixtureId: 'f', stage: 'group', group: 'B', round: 2, tie: 1, match: 'winners' } })
  expect(screen.getByRole('link', { name: 'Autumn cup' })).toHaveAttribute('href', '/leagues/league-1')
  expect(screen.getByText(/GROUP B · WINNERS' MATCH/)).toBeInTheDocument()
})

it('names the knockout stage and leaves other battles unlinked', () => {
  const view = header({ ...battle, league: { id: 'league-1', name: 'Autumn cup', fixtureId: 'f', stage: 'final', round: 1, tie: 1 } })
  expect(screen.getByText(/FINAL · TIE 1/)).toBeInTheDocument()
  view.unmount()
  header(battle)
  expect(screen.queryByText(/LEAGUE/)).not.toBeInTheDocument()
})

it('names who started the battle, and nobody when the name is unavailable', () => {
  const view = header({ ...battle, ownerId: 'issuer|ada', ownerName: 'Ada' })
  expect(screen.getByText('Started by', { exact: false })).toHaveTextContent('Started by Ada')
  expect(screen.queryByText('issuer|ada')).not.toBeInTheDocument()
  view.unmount()
  header(battle)
  expect(screen.queryByText(/Started by/)).not.toBeInTheDocument()
})

it('names the winner with its model, and a scripted winner without one', () => {
  const view = render(<Outcome battle={completed} winnerName="Jov" />)
  expect(screen.getByText(/Jov wins/)).toHaveTextContent('Jov wins gpt-6.1-sol · high')
  view.unmount()

  const scriptedWin = { ...completed, result: { ...completed.result!, winnerFactionId: 'ws' } }
  render(<Outcome battle={scriptedWin} winnerName="WS" />)
  expect(screen.getByText('WS wins').querySelector('.faction-model')).toBeNull()
})

it('heads each faction statistics card with its model and marks the winner', () => {
  render(<BattleStatistics battle={completed} participantNames={names} />)
  expect(screen.getByRole('heading', { name: 'Jov gpt-6.1-sol · high Winner' })).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: 'WS' })).toBeInTheDocument()
})

it('marks no winner after a draw', () => {
  render(<BattleStatistics battle={{ ...completed, result: { ...completed.result!, winnerFactionId: null } }} participantNames={names} />)
  expect(screen.queryByText('Winner')).not.toBeInTheDocument()
})
