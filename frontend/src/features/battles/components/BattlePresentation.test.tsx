import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, it } from 'vitest'
import type { BattleRun } from '../api/battles'
import { BattleHeader } from './BattlePresentation'

const battle = { id: 'b', scenarioId: 'frontier-siege', attempt: 1, status: 'running' } as BattleRun

function header(run: BattleRun) {
  return render(<MemoryRouter><BattleHeader battle={run} battleTitle="Battle in progress" connection="live" connectionMessage="" retryConnection={() => undefined} /></MemoryRouter>)
}

it('links a league battle to its league and fixture', () => {
  header({ ...battle, league: { id: 'league-1', name: 'Autumn cup', fixtureId: 'f', stage: 'group', group: 'B', round: 2, leg: 1 } })
  expect(screen.getByRole('link', { name: 'Autumn cup' })).toHaveAttribute('href', '/leagues/league-1')
  expect(screen.getByText(/GROUP B · LEG 1/)).toBeInTheDocument()
})

it('names the knockout stage and leaves other battles unlinked', () => {
  const view = header({ ...battle, league: { id: 'league-1', name: 'Autumn cup', fixtureId: 'f', stage: 'final', round: 1, tie: 1, leg: 2 } })
  expect(screen.getByText(/FINAL · LEG 2/)).toBeInTheDocument()
  view.unmount()
  header(battle)
  expect(screen.queryByText(/LEAGUE/)).not.toBeInTheDocument()
})
