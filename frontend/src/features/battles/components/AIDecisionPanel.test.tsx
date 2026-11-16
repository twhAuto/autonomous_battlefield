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

it('names ordered upgrades, recruits and construction beside their icons', () => {
  show({
    upgradeOrders: ['industry_2', 'gunpowder', 'ranger_longshot'],
    recruitmentOrders: [{ unitType: 'cannon', count: 1 }],
    constructionOrders: [{ buildingType: 'stables', position: { x: 3, y: 4 } }, { buildingType: 'economy', position: { x: 5, y: 1 } }],
  })
  const row = (label: string) => screen.getByText(label).nextElementSibling!
  expect(row('Upgrades')).toHaveTextContent('Industry II → Gunpowder (unlocks Cannon, Gunner) → Ranger: Longshot (+1 range)')
  expect(row('Upgrades').querySelectorAll('svg')).toHaveLength(4)
  expect(row('Recruit').querySelectorAll('svg')).toHaveLength(1)
  expect(row('Build sites')).toHaveTextContent('Stables (3,4) → Economy (5,1)')
  expect(row('Build sites').querySelectorAll('svg')).toHaveLength(2)
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
    expect(row.getByText('Input total').nextElementSibling).toHaveTextContent('100')
    expect(row.getByText('Output total').nextElementSibling).toHaveTextContent('20')
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

it('shows CLI aggregate usage and a trace link without inventing model calls', () => {
  const traceUrl = 'http://localhost:13000/project/battlefield-agents/traces/abc'
  render(<AIDecisionPanel decisions={[{ requestId: 'r', factionId: 'a', targetTick: 10, status: 'accepted', controllerType: 'model', usage: { scope: 'cli_session', tokensReported: true, inputTokens: 30000, cachedInputTokens: 25000, reasoningOutputTokens: 100, cliInvocations: 1 }, traceUrl, createdAt: '' }]} participantNames={new Map()} message="" hasModelController />)
  expect(screen.getByText('Input total').nextElementSibling).toHaveTextContent('30000')
  expect(screen.getByText('Uncached input').nextElementSibling).toHaveTextContent('5000')
  expect(screen.getByText('Output total').nextElementSibling).toHaveTextContent('0')
  expect(screen.getByText('CLI session total')).toBeVisible()
  expect(screen.getByText('Cached input').nextElementSibling).toHaveTextContent('25000')
  expect(screen.getByText('Cost basis').nextElementSibling).toHaveTextContent('CLI account usage; not a direct API cost')
  expect(screen.getByText('Model calls').nextElementSibling).toHaveTextContent('—')
  expect(screen.getByRole('link', { name: 'View in Langfuse' })).toHaveAttribute('href', traceUrl)
  expect(screen.queryByText('Requests (legacy)')).not.toBeInTheDocument()
})

it('shows the model the provider ran beside the selected model', () => {
  const base: DecisionHistory = { requestId: 'r', factionId: 'a', targetTick: 10, status: 'accepted', controllerType: 'model', modelIdentifier: 'opus', usage: {}, createdAt: '' }
  const { rerender } = render(<AIDecisionPanel decisions={[{ ...base, resolvedModel: 'claude-opus-5-5' }]} participantNames={new Map()} message="" hasModelController />)
  expect(screen.getByText('Model').nextElementSibling).toHaveTextContent('opus')
  expect(screen.getByText('Model run').nextElementSibling).toHaveTextContent('claude-opus-5-5')
  rerender(<AIDecisionPanel decisions={[base]} participantNames={new Map()} message="" hasModelController />)
  expect(screen.getByText('Model run').nextElementSibling).toHaveTextContent('Not reported')
})

it('labels API usage as summed model calls', () => {
  render(<AIDecisionPanel decisions={[{ requestId: 'r', factionId: 'a', targetTick: 10, status: 'accepted', controllerType: 'model', usage: { scope: 'api_calls', tokensReported: true, modelCalls: 3 }, createdAt: '' }]} participantNames={new Map()} message="" hasModelController />)
  expect(screen.getByText('Input total').nextElementSibling).toHaveTextContent('0')
  expect(screen.getByText('Uncached input').nextElementSibling).toHaveTextContent('—')
  expect(screen.getByText('Output total').nextElementSibling).toHaveTextContent('0')
  expect(screen.getByText('Sum of API calls')).toBeVisible()
  expect(screen.getByText('Cost basis').nextElementSibling).toHaveTextContent('Direct API usage; priced by model')
  expect(screen.getByText('Model calls').nextElementSibling).toHaveTextContent('3')
  expect(screen.queryByRole('link', { name: 'View in Langfuse' })).not.toBeInTheDocument()
})

it('shows persistent selections and off orders with source names', () => {
  show({ productionOrders: [{ buildingId: 'a:castle', unitType: 'vanguard', enabled: true }, { buildingId: 'a:barracks:1', enabled: false }] })
  expect(screen.getByText('Production').nextElementSibling).toHaveTextContent('Castle: On (Vanguard) · Barracks #1: Off')
})

it('shows an empty production decision as unchanged settings', () => {
  show({ productionOrders: [] })
  expect(screen.getByText('Keep current settings')).toBeVisible()
})
