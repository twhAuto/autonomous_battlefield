// Checks explicit recruitment quantities, empty purchase orders, and compatibility with
// older saved decision plans.

import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { AIDecisionPanel } from './AIDecisionPanel'
import type { DecisionHistory } from '../api/battles'

function show(plan: DecisionHistory['resolvedPlan']) {
  render(<AIDecisionPanel decisions={[{ requestId: 'r', factionId: 'a', targetTick: 10, status: 'accepted', controllerType: 'model', resolvedPlan: plan, usage: {}, createdAt: '' }]} participantNames={new Map([['a', 'Blue']])} message="" hasModelController />)
}
describe('explicit recruitment display', () => {
  it('shows ordered quantities', () => {
    show({ recruitmentOrders: [{ unitType: 'ranger', count: 2 }, { unitType: 'vanguard', count: 1 }] })
    expect(screen.getByText('2 × ranger → 1 × vanguard')).toBeInTheDocument()
  })
  it('distinguishes no purchases from legacy priorities', () => {
    show({ recruitmentOrders: [], recruitmentPriorities: ['ranger'] })
    expect(screen.getByText('No purchases')).toBeInTheDocument()
  })
  it('renders older saved plans', () => {
    show({ recruitmentPriorities: ['ranger', 'vanguard'] })
    expect(screen.getByText('ranger → vanguard')).toBeInTheDocument()
  })
})
