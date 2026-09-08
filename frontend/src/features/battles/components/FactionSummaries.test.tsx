import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { BattleRun, DecisionHistory } from '../api/battles'
import { FactionSummaries } from './FactionSummaries'

const battle: BattleRun = {
  id: 'battle', scenarioId: 'siege', scenarioDigest: '', requestedTicks: 100, currentTick: 12,
  status: 'running', paused: false, attempt: 1, result: null,
  participants: [{ id: 'a', name: 'Amber', controller: { type: 'model' } }],
  state: { battleId: 'battle', tick: 12, factions: [{ factionId: 'a', gold: 0, goldEarned: 0, goldSpent: 0, goldLooted: 0 }], units: [], buildings: [] },
  map: { kind: 'authored', width: 1, height: 1, rows: ['.'], castleSpawns: [{ x: 0, y: 0 }, { x: 0, y: 0 }], economySites: [[], []] },
}
const decision: DecisionHistory = { requestId: '1', factionId: 'a', targetTick: 12, status: 'running', controllerType: 'model', usage: {}, createdAt: '' }
const props = { battle, decisions: [decision], decisionMessage: '' }

describe('faction summaries', () => {
  it.each([
    ['pending', 'Queued'], ['running', 'Deciding'], ['accepted', 'Plan ready'],
    ['fallback', 'Fallback'], ['failed', 'Decision failed'],
  ] as const)('shows the latest %s decision for its faction', (status, label) => {
    render(<FactionSummaries {...props} decisions={[decision, { ...decision, requestId: '2', status }, { ...decision, factionId: 'other' }]} />)
    expect(screen.getByRole('article', { name: 'Amber' })).toHaveTextContent(label)
  })

  it('updates activity without dropping metrics and reports polling failure', () => {
    const { rerender } = render(<FactionSummaries {...props} decisions={[]} />)
    expect(screen.getByText('Awaiting decision')).toBeVisible()
    rerender(<FactionSummaries {...props} />)
    expect(screen.getByText('Deciding')).toBeVisible()
    rerender(<FactionSummaries {...props} decisionMessage="Request failed" />)
    expect(screen.getByText('Status unavailable')).toBeVisible()
    expect(screen.queryByText('Deciding')).not.toBeInTheDocument()
    expect(screen.getAllByText('0')).toHaveLength(2)
  })

  it.each([
    ['completed', false, 'Completed'], ['failed', false, 'Battle failed'],
    ['stopped', false, 'Stopped'], ['running', true, 'Paused'], ['queued', true, 'Paused'],
  ] as const)('overrides stale activity when %s (paused=%s)', (status, paused, label) => {
    render(<FactionSummaries {...props} battle={{ ...battle, status, paused }} decisionMessage="Request failed" />)
    expect(screen.getByText(label)).toBeVisible()
    expect(screen.queryByText('Deciding')).not.toBeInTheDocument()
  })

  it('identifies explicit and legacy scripted controllers despite a decision polling failure', () => {
    render(<FactionSummaries {...props} battle={{ ...battle, participants: [{ id: 'a', name: 'Amber', controller: { type: 'scripted' } }, { id: 'b', name: 'Blue' }] }} decisionMessage="Request failed" />)
    expect(screen.getAllByText('Scripted')).toHaveLength(2)
  })

  it('counts living units belonging to the faction', () => {
    const unit = { id: 'u', factionId: 'a', type: 'soldier', position: { x: 0, y: 0 }, health: 10, maxHealth: 10, attack: 1, defense: 1, speed: 1, movement: 1, range: 1, vision: 1, readyAtTick: 0 }
    render(<FactionSummaries {...props} battle={{ ...battle, state: { ...battle.state, units: [unit, { ...unit, id: 'dead', health: 0 }, { ...unit, id: 'enemy', factionId: 'b' }] } }} />)
    expect(screen.getByText('Units').nextElementSibling).toHaveTextContent('1')
    expect(screen.getByText('Gold').nextElementSibling).toHaveTextContent('0')
  })

  it('shows placeholders for missing metrics, distinguishing missing units from zero', () => {
    const { rerender } = render(<FactionSummaries {...props} battle={{ ...battle, state: { ...battle.state, factions: null, units: null } }} />)
    expect(screen.getAllByText('—')).toHaveLength(2)
    rerender(<FactionSummaries {...props} battle={{ ...battle, state: { ...battle.state, units: null } }} />)
    expect(screen.getByText('Units').nextElementSibling).toHaveTextContent('—')
    rerender(<FactionSummaries {...props} />)
    expect(screen.getAllByText('0')).toHaveLength(2)
  })

  it.each([2, 3, 4, 6])('renders all %s participants in order, including long names', (count) => {
    const participants = Array.from({ length: count }, (_, index) => ({ id: String(index), name: `Faction ${index} with a very long name` }))
    render(<FactionSummaries {...props} battle={{ ...battle, participants }} />)
    const cards = within(screen.getByRole('region', { name: 'Faction summaries' })).getAllByRole('article')
    expect(cards).toHaveLength(count)
    cards.forEach((card, index) => expect(within(card).getByRole('heading')).toHaveTextContent(participants[index].name))
  })
})
