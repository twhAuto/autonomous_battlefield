import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'
import { HexBattleMap } from './HexBattleMap'
import { AIDecisionPanel } from './AIDecisionPanel'
import type { BattleMap, BattleState, BattleUnit, BattleBuilding, DecisionHistory } from '../api/battles'

it('shows the expanded roster, distinct structures, treatment, and tower attacks on a large map', async () => {
  const user = userEvent.setup()
  const map: BattleMap = { kind: 'authored', width: 25, height: 17, rows: Array.from({ length: 17 }, () => '.'.repeat(25)), castleSpawns: [{ x: 2, y: 8 }, { x: 22, y: 8 }], economySites: [[], []] }
  const unit = (type: string, x: number): BattleUnit => ({ id: type, type, factionId: 'a', position: { x, y: 8 }, health: 25, maxHealth: 40, attack: 4, defense: 1, speed: 2, movement: 1, range: 5, vision: 5, readyAtTick: 1 })
  const building = (type: BattleBuilding['type'], x: number): BattleBuilding => ({ id: type, type, factionId: 'a', position: { x, y: 7 }, health: 50, maxHealth: 100, defense: 3, vision: 6, income: 0 })
  const state: BattleState = { battleId: 'test', tick: 5, factions: [{ factionId: 'a', gold: 100, goldEarned: 100, goldSpent: 0, goldLooted: 0 }], units: [
    { ...unit('catapult', 5), minimumRange: 2, buildingDamageBonus: 20 },
    { ...unit('pikeman', 6), range: 1, bonusAgainstClass: 'mobile', classDamageBonus: 12 },
    { ...unit('herbalist', 7), attack: 0, healAmount: 3, healTargetId: 'pikeman', healTargetPosition: { x: 6, y: 8 } },
    unit('battering_ram', 8),
  ], buildings: [building('watchtower', 7), building('barracks', 9), building('economy', 4), { ...building('defense_tower', 8), attack: 12, range: 3, attackTargetId: 'enemy', attackTargetPosition: { x: 10, y: 7 } }] }
  const { container, rerender } = render(<HexBattleMap map={map} state={state} visiblePositions={[]} factionNames={new Map([['a', 'Northwind']])} />)
  expect(container.querySelectorAll('.hex-tile')).toHaveLength(425)
  expect(container.querySelectorAll('.unit-direction--healing')).toHaveLength(1)
  expect(container.querySelectorAll('.unit-direction--tower')).toHaveLength(1)
  expect(screen.getByText('Northwind · Herbalist · herbalis treated Northwind · Pikeman · pikeman')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Northwind · Herbalist · herbalis' }))
  expect(screen.getByText(/treating Northwind · Pikeman · pikeman.*Treat 3 HP/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Northwind · Catapult · catapult' }))
  expect(screen.getByText(/Range 2–5.*\+20 vs buildings/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Northwind · Pikeman · pikeman' }))
  expect(screen.getByText(/\+12 vs mobile/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Northwind · Barracks · barracks' }))
  expect(screen.getByText(/Forward recruitment/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Northwind · Watchtower · watchtow' }))
  expect(screen.getByText(/Vision 6/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Northwind · Defense Tower · defense_' }))
  expect(screen.getByText(/attacking Entity · enemy.*Attack 12/)).toBeInTheDocument()
  // The next persisted tick has no transient action indicators.
  const next = structuredClone(state)
  delete next.units![2].healTargetPosition
  delete next.buildings![3].attackTargetPosition
  rerender(<HexBattleMap map={map} state={next} visiblePositions={[]} factionNames={new Map()} />)
  expect(container.querySelectorAll('.unit-direction--healing, .unit-direction--tower')).toHaveLength(0)
})

it('shows typed construction and forward recruitment in decision history', () => {
  const decision: DecisionHistory = { requestId: 'request', factionId: 'a', targetTick: 10, status: 'accepted', controllerType: 'model', usage: {}, createdAt: '', resolvedPlan: { name: 'fortify', constructionOrders: [{ buildingType: 'defense_tower', position: { x: 9, y: 7 } }], recruitmentOrders: [{ unitType: 'catapult', count: 1, sourceBuildingId: 'a:barracks:1' }] } }
  const props = { decisions: [decision], participantNames: new Map(), message: '', hasModelController: true }
  const { rerender } = render(<AIDecisionPanel {...props} />)
  expect(screen.getByText('Defense Tower (9,7)')).toBeInTheDocument()
  expect(screen.getByText('Recruit').nextElementSibling).toHaveTextContent('1 × Catapult from Barracks #1')
  expect(screen.getByText('Barracks #1')).toHaveAttribute('title', 'a:barracks:1')
  decision.resolvedPlan!.constructionOrders = []
  rerender(<AIDecisionPanel {...props} />)
  expect(screen.getByText('No construction')).toBeInTheDocument()
})
