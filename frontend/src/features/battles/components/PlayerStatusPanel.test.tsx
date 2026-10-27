import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { BattleState, BattleUnit } from '../api/battles'
import { playerStatus, PlayerMetrics, PlayerOverview, PlayerUnits } from './PlayerStatusPanel'

const participants = [{ id: 'a', name: 'Amber' }, { id: 'b', name: 'Blue' }, { id: 'c', name: 'Crimson' }]
const unit = (id: string, factionId: string, health: number): BattleUnit => ({ id, factionId, health, type: 'ranger', position: { x: 1, y: 1 }, maxHealth: 10, attack: 1, defense: 1, speed: 1, movement: 1, range: 1, vision: 1, readyAtTick: 0 })
const state: BattleState = {
  battleId: 'battle', tick: 42,
  factions: participants.map(({ id }, index) => ({ factionId: id, gold: index * 20, goldEarned: 100, goldSpent: 80, goldLooted: 5, ownedUpgradeIds: index === 2 ? ['steel'] : [] })),
  units: [unit('a1', 'a', 10), unit('a2', 'a', 0), unit('c1', 'c', 7), unit('c2', 'c', 8)],
  buildings: [{ id: 'castle', factionId: 'c', type: 'castle', position: { x: 0, y: 0 }, health: 90, maxHealth: 100, defense: 1, vision: 1, income: 1 }, { id: 'ruin', factionId: 'c', type: 'economy', position: { x: 1, y: 0 }, health: 0, maxHealth: 10, defense: 1, vision: 1, income: 1 }],
}

function Panel({ state: snapshot }: Readonly<{ state?: BattleState }>) {
  return <>{participants.map((player) => {
    const status = playerStatus(snapshot, player.id)
    return <article aria-label={`${player.name} status`} key={player.id}><PlayerMetrics status={status} /><PlayerOverview status={status} /><PlayerUnits status={status} /></article>
  })}</>
}

describe('player status', () => {
  it('groups living units and buildings and exposes individual HP and coordinates', async () => {
    render(<Panel state={state} />)
    expect(screen.getAllByRole('article')).toHaveLength(3)
    const crimson = within(screen.getByRole('article', { name: 'Crimson status' }))
    expect(crimson.getByText('Gold').nextElementSibling).toHaveTextContent('40')
    expect(crimson.getByText('Units on field').nextElementSibling).toHaveTextContent('2')
    expect(crimson.getByText('Buildings').nextElementSibling).toHaveTextContent('1')
    expect(crimson.getByText('90 / 100')).toBeInTheDocument()
    expect(crimson.getByText('steel')).toBeVisible()
    expect(within(screen.getByRole('article', { name: 'Amber status' })).getByText('Owned upgrades').nextElementSibling).toHaveTextContent('None')
    await userEvent.click(crimson.getByText('2 × Ranger'))
    expect(crimson.getByText('7 / 10')).toBeVisible()
    expect(crimson.getByText('Ranger · c1')).toBeVisible()
    expect(crimson.getAllByText('(1, 1)')).toHaveLength(2)
    expect(screen.queryByText('a2')).not.toBeInTheDocument()
  })

  it('handles unavailable and null opening state then updates after recruitment', () => {
    const { rerender } = render(<Panel />)
    const crimson = within(screen.getByRole('article', { name: 'Crimson status' }))
    expect(crimson.getByText('Gold').nextElementSibling).toHaveTextContent('—')
    rerender(<Panel state={{ ...state, units: null, buildings: null, factions: null }} />)
    expect(crimson.getByText('Units on field').nextElementSibling).toHaveTextContent('—')
    expect(crimson.getByText('Owned upgrades').nextElementSibling).toHaveTextContent('—')
    rerender(<Panel state={{ ...state, units: null, buildings: null }} />)
    expect(crimson.getByText('Units on field').nextElementSibling).toHaveTextContent('0')
    expect(crimson.getByText('No units on the field')).toBeInTheDocument()
    expect(crimson.getByText('No living buildings')).toBeInTheDocument()
    rerender(<Panel state={state} />)
    expect(crimson.getByText('Units on field').nextElementSibling).toHaveTextContent('2')
    const amber = within(screen.getByRole('article', { name: 'Amber status' }))
    expect(amber.getByText('Gold').nextElementSibling).toHaveTextContent('0')
    expect(amber.getByText('Destroyed')).toBeInTheDocument()
  })
})

it('lists the highest owned upgrade per track with icons', () => {
  const factions = [{ factionId: 'a', gold: 0, goldEarned: 0, goldSpent: 0, goldLooted: 0, ownedUpgradeIds: ['arsenal', 'arsenal_2', 'siege_breach'] }]
  render(<PlayerOverview status={playerStatus({ ...state, factions }, 'a')} />)
  const items = within(screen.getByText('Owned upgrades').nextElementSibling as HTMLElement).getAllByRole('listitem')
  expect(items.map((item) => item.textContent)).toEqual(['Arsenal II', 'Siege: Breach (+8 building damage)'])
  expect(items[1].querySelectorAll('svg')).toHaveLength(2)
})

it('keeps roster numbers after recruitment, deaths, reordering, and remounting', async () => {
  const first = unit('a:ranger:012', 'a', 10)
  const second = unit('a:ranger:013', 'a', 10)
  const { rerender, unmount } = render(<PlayerUnits status={playerStatus({ ...state, units: [first, second] }, 'a')} />)
  await userEvent.click(screen.getByText('2 × Ranger'))
  expect(screen.getByText('Ranger #12')).toHaveAttribute('title', first.id)
  rerender(<PlayerUnits status={playerStatus({ ...state, units: [unit('a:ranger:014', 'a', 10), { ...second, health: 0 }, first] }, 'a')} />)
  expect(screen.getByText('Ranger #12')).toBeVisible()
  expect(screen.getByText('Ranger #14')).toBeVisible()
  expect(screen.queryByText('Ranger #13')).not.toBeInTheDocument()
  unmount()
  render(<PlayerUnits status={playerStatus({ ...state, units: [first] }, 'a')} />)
  await userEvent.click(screen.getByText('1 × Ranger'))
  expect(screen.getByText('Ranger #12')).toBeVisible()
})
