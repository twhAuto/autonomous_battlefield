// Checks explicit recruitment quantities, empty purchase orders, and compatibility with
// older saved decision plans.

import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { AIDecisionPanel, decisionLabel } from './AIDecisionPanel'
import type { DecisionHistory } from '../api/battles'

function show(plan: DecisionHistory['resolvedPlan']) {
  render(<AIDecisionPanel decisions={[{ requestId: 'r', factionId: 'a', targetTick: 10, status: 'accepted', controllerType: 'model', resolvedPlan: plan, usage: {}, createdAt: '' }]} participantNames={new Map([['a', 'Blue']])} message="" hasModelController />)
}
describe('explicit recruitment display', () => {
  it('shows ordered quantities', () => {
    show({ recruitmentOrders: [{ unitType: 'ranger', count: 2 }, { unitType: 'vanguard', count: 1 }] })
    expect(screen.getByText('2 × Ranger → 1 × Vanguard')).toBeInTheDocument()
  })
  it('distinguishes no purchases from legacy priorities', () => {
    show({ recruitmentOrders: [], recruitmentPriorities: ['ranger'] })
    expect(screen.getByText('No purchases')).toBeInTheDocument()
  })
  it('renders older saved plans', () => {
    show({ recruitmentPriorities: ['ranger', 'vanguard'] })
    expect(screen.getByText('Ranger → Vanguard')).toBeInTheDocument()
  })
})

describe('player decision inspector', () => {
  const participants = new Map([['a', 'Amber'], ['b', 'Blue'], ['c', 'Crimson']])
  const decision: DecisionHistory = { requestId: 'a1', factionId: 'a', targetTick: 0, status: 'accepted', controllerType: 'model', resolvedPlan: { name: 'Opening', rationale: 'Build an economy first.' }, usage: {}, createdAt: '' }
  const third: DecisionHistory = { ...decision, requestId: 'c1', factionId: 'c', targetTick: 10, status: 'fallback', fallbackSource: 'previous_plan', errorCategory: 'timeout', provider: 'provider', runtime: 'api', resolvedAt: '2026-09-07T00:00:10Z', createdAt: '2026-09-07T00:00:00Z', usage: { inputTokens: 100, outputTokens: 20, requests: 2 }, resolvedPlan: { name: 'Hold', rationale: 'Protect the damaged castle.', targetBonuses: { castle: 1200 }, constructionOrders: [], upgradeOrders: ['steel'] } }

  it('inspects a third player and keeps fallback provenance, usage, and rationale visible', async () => {
    render(<AIDecisionPanel decisions={[decision, third]} participantNames={participants} message="" hasModelController attempt={2} />)
    await userEvent.selectOptions(screen.getByLabelText('Player'), 'c')
    const row = within(screen.getByRole('listitem'))
    expect(row.getByText('Protect the damaged castle.')).toBeVisible()
    expect(row.getByText('previous_plan')).toBeVisible()
    expect(row.getByText('timeout')).toBeVisible()
    expect(row.getByText('100 / 20')).toBeVisible()
    expect(row.getByText('Castle 1200')).toBeVisible()
    expect(row.getByText('No construction')).toBeVisible()
    expect(screen.getByText('AI DECISION STREAM · ATTEMPT 2')).toBeInTheDocument()
    expect(screen.queryByText('Build an economy first.')).not.toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText('Player'), 'b')
    expect(screen.getByText('No decisions recorded for this player in the current attempt.')).toBeInTheDocument()
  })

  it('lets the operator expand an older decision and inspect scripted decisions by player', async () => {
    render(<AIDecisionPanel decisions={[decision, third, { ...decision, requestId: 'b1', factionId: 'b', controllerType: 'scripted', resolvedPlan: undefined }]} participantNames={participants} message="" hasModelController />)
    expect(screen.getAllByRole('listitem')).toHaveLength(2)
    expect(screen.getByText('Build an economy first.')).not.toBeVisible()
    await userEvent.click(screen.getByText('OPENING'))
    expect(screen.getByText('Build an economy first.')).toBeVisible()
    await userEvent.selectOptions(screen.getByLabelText('Player'), 'b')
    expect(screen.getByText('scripted')).toBeVisible()
    expect(screen.getByText('No rationale recorded.')).toBeVisible()
  })

  it('describes recorded lifecycle states without implying connection telemetry', () => {
    expect(decisionLabel({ ...decision, status: 'pending' })).toBe('queued for analysis')
    expect(decisionLabel({ ...decision, status: 'running' })).toBe('decision in progress')
    expect(decisionLabel({ ...decision, status: 'failed', errorCategory: 'timeout' })).toBe('decision failed: timeout')
  })
})

it('uses metadata for opaque recruitment sources and leaves freeform rationale intact', () => {
  const id = '0c944652-691e-4b82-9c82-e4ee5fcf92cd'
  const entities = new Map([[id, { id, type: 'barracks', factionId: 'a' }]])
  const rationale = `Recruit a:ranger:012 from ${id}`
  render(<AIDecisionPanel entities={entities} decisions={[{ requestId: 'r', factionId: 'a', targetTick: 1, status: 'accepted', controllerType: 'model', usage: {}, createdAt: '', resolvedPlan: {
    rationale, recruitmentOrders: [{ unitType: 'battering_ram', count: 2, sourceBuildingId: id }],
  } }]} participantNames={new Map([['a', 'Amber']])} message="" hasModelController />)
  expect(screen.getByText('Recruit').nextElementSibling).toHaveTextContent('2 × Battering Ram from Barracks · 0c944652')
  expect(screen.getByText('Barracks · 0c944652')).toHaveAttribute('title', id)
  expect(screen.getByText(rationale)).toBeInTheDocument()
})
