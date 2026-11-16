import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { BattleBuilding, BattleRun, DecisionHistory } from '../api/battles'
import { SessionContext } from '../../auth/SessionContext'
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
    expect(screen.getAllByText('0')).toHaveLength(3)
  })

  it.each([['running', true], ['queued', true]] as const)('overrides stale activity when %s (paused=%s)', (status, paused) => {
    render(<FactionSummaries {...props} battle={{ ...battle, status, paused }} decisionMessage="Request failed" />)
    expect(screen.getByText('Paused')).toBeVisible()
    expect(screen.queryByText('Deciding')).not.toBeInTheDocument()
  })

  it.each(['completed', 'failed', 'stopped'] as const)('shows no activity once the battle is %s', (status) => {
    render(<FactionSummaries {...props} battle={{ ...battle, status }} decisionMessage="Request failed" />)
    const summary = within(screen.getByRole('article', { name: 'Amber' })).getByText('Plan unavailable').closest('summary')
    expect(summary).toHaveTextContent(/^\s*Plan unavailable$/)
    expect(summary!.querySelector('.faction-summary__activity')).toBeNull()
  })

  it('identifies explicit and legacy scripted controllers despite a decision polling failure', () => {
    render(<FactionSummaries {...props} battle={{ ...battle, participants: [{ id: 'a', name: 'Amber', controller: { type: 'scripted' } }, { id: 'b', name: 'Blue' }] }} decisionMessage="Request failed" />)
    expect(screen.getAllByText('Scripted')).toHaveLength(2)
  })

  it('shows the model and any reasoning effort beside the faction name', () => {
    render(<FactionSummaries {...props} battle={{ ...battle, participants: [
      { id: 'a', name: 'Amber', controller: { type: 'model', profileId: 'claude', modelIdentifier: 'haiku' } },
      { id: 'b', name: 'Blue', controller: { type: 'model', profileId: 'codex', modelIdentifier: 'gpt-6-luna', reasoningEffort: 'low' } },
      { id: 'c', name: 'Cyan', controller: { type: 'scripted' } },
      { id: 'd', name: 'Dusk', controller: { type: 'model' } },
    ] }} />)
    const heading = (name: string) => within(screen.getByRole('article', { name })).getByRole('heading')
    expect(heading('Amber')).toHaveTextContent('Amberhaiku')
    expect(heading('Amber')).toHaveAttribute('title', 'Amber (haiku)')
    expect(heading('Blue')).toHaveTextContent('Bluegpt-6-luna · low')
    expect(heading('Cyan')).toHaveTextContent(/^Cyan$/)
    expect(heading('Dusk')).toHaveTextContent(/^Dusk$/)
    expect(heading('Dusk')).toHaveAttribute('title', 'Dusk')
  })

  it('adds the model the latest reporting decision ran when it differs from the selection', () => {
    const participants = [
      { id: 'a', name: 'Amber', controller: { type: 'model' as const, profileId: 'claude', modelIdentifier: 'opus', reasoningEffort: 'low' } },
      { id: 'b', name: 'Blue', controller: { type: 'model' as const, profileId: 'claude-api', modelIdentifier: 'claude-opus-5-5' } },
    ]
    render(<FactionSummaries {...props} battle={{ ...battle, participants }} decisions={[
      { ...decision, factionId: 'a', resolvedModel: 'claude-opus-5' },
      { ...decision, requestId: '2', factionId: 'a' },
      { ...decision, requestId: '3', factionId: 'b', resolvedModel: 'claude-opus-5-5' },
    ]} />)
    const heading = (name: string) => within(screen.getByRole('article', { name })).getByRole('heading')
    expect(heading('Amber')).toHaveTextContent('Amberopus (claude-opus-5) · low')
    expect(heading('Amber')).toHaveAttribute('title', 'Amber (opus (claude-opus-5) · low)')
    expect(heading('Blue')).toHaveTextContent(/^Blueclaude-opus-5-5$/)
  })

  it('counts living units belonging to the faction', () => {
    const unit = { id: 'u', factionId: 'a', type: 'soldier', position: { x: 0, y: 0 }, health: 10, maxHealth: 10, attack: 1, defense: 1, speed: 1, movement: 1, range: 1, vision: 1, readyAtTick: 0 }
    render(<FactionSummaries {...props} battle={{ ...battle, state: { ...battle.state, units: [unit, { ...unit, id: 'dead', health: 0 }, { ...unit, id: 'enemy', factionId: 'b' }] } }} />)
    expect(screen.getByText('Units').nextElementSibling).toHaveTextContent('1')
    expect(screen.getByText('Gold').nextElementSibling).toHaveTextContent('0')
  })

  it('counts units in training against the unit cap and buildings other than the castle', () => {
    const unit = { id: 'u', factionId: 'a', type: 'striker', position: { x: 0, y: 0 }, health: 10, maxHealth: 10, attack: 1, defense: 1, speed: 1, movement: 1, range: 1, vision: 1, readyAtTick: 0 }
    const building = (id: string, type: BattleBuilding['type'], health = 50): BattleBuilding => ({ id, factionId: 'a', type, position: { x: 0, y: 0 }, health, maxHealth: 50, defense: 1, vision: 1, income: 0 })
    const queue = [{ unitType: 'striker', remainingTicks: 2, trainingTicks: 4, queuedAtTick: 10 }]
    const state = { ...battle.state, units: [unit, { ...unit, id: 'v' }], buildings: [
      { ...building('a:castle', 'castle'), trainingQueue: queue }, building('a:barracks:1', 'barracks'), building('a:economy:2', 'economy'),
      { ...building('a:barracks:3', 'barracks', 0), trainingQueue: queue }, { ...building('b:barracks:1', 'barracks'), factionId: 'b' },
    ] }
    const { rerender } = render(<FactionSummaries {...props} battle={{ ...battle, maximumUnits: 40, state }} />)
    const units = screen.getByText('Units').parentElement!
    expect(units).toHaveTextContent('3 / 40')
    expect(units).toHaveAttribute('title', '2 on the field, 1 in training; cap 40')
    expect(screen.getByText('Buildings').nextElementSibling).toHaveTextContent(/^2$/)
    rerender(<FactionSummaries {...props} battle={{ ...battle, state }} />)
    expect(screen.getByText('Units').nextElementSibling).toHaveTextContent(/^3$/)
    expect(screen.getByText('Units').parentElement).toHaveAttribute('title', '2 on the field, 1 in training')
  })

  it('shows placeholders for missing metrics, distinguishing missing units from zero', () => {
    const { rerender } = render(<FactionSummaries {...props} battle={{ ...battle, state: { ...battle.state, factions: null, units: null } }} />)
    expect(screen.getAllByText('—')).toHaveLength(4)
    rerender(<FactionSummaries {...props} battle={{ ...battle, state: { ...battle.state, units: null } }} />)
    expect(screen.getByText('Units').nextElementSibling).toHaveTextContent('—')
    rerender(<FactionSummaries {...props} />)
    expect(screen.getAllByText('0')).toHaveLength(3)
  })

  it.each([2, 3, 4, 6])('renders all %s participants in order, including long names', (count) => {
    const participants = Array.from({ length: count }, (_, index) => ({ id: String(index), name: `Faction ${index} with a very long name` }))
    render(<FactionSummaries {...props} battle={{ ...battle, participants }} />)
    const cards = within(screen.getByRole('region', { name: 'Faction summaries' })).getAllByRole('article')
    expect(cards).toHaveLength(count)
    cards.forEach((card, index) => expect(within(card).getByRole('heading')).toHaveTextContent(participants[index].name))
  })
})

 it('keeps the applied plan inside its faction box while a future decision is pending or ready', () => {
  const applied = { ...decision, targetTick: 10, status: 'accepted' as const, resolvedPlan: { name: 'Hold the bridge', rationale: 'Protect the economy.' } }
  render(<FactionSummaries {...props} decisions={[applied, { ...applied, factionId: 'other', resolvedPlan: { name: 'Enemy plan' } }, { ...applied, targetTick: 20, resolvedPlan: { name: 'Future plan' } }, { ...decision, targetTick: 30 }]} />)
  const card = within(screen.getByRole('article', { name: 'Amber' }))
  expect((card.getByRole('textbox') as HTMLTextAreaElement).value).toContain('Hold the bridge')
  expect((card.getByRole('textbox') as HTMLTextAreaElement).value).toContain('Protect the economy.')
  expect((card.getByRole('textbox') as HTMLTextAreaElement).value).not.toContain('Future plan')
  expect((card.getByRole('textbox') as HTMLTextAreaElement).value).not.toContain('Enemy plan')
  expect(card.getByText('Deciding')).toBeVisible()
 })

 it('shows fallback rationale after stopping and clear empty states', () => {
  const { rerender } = render(<FactionSummaries {...props} decisions={[]} />)
  expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toContain('No plan applied yet')
  expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toContain('No rationale recorded.')
  rerender(<FactionSummaries {...props} decisions={[]} decisionMessage="offline" />)
  expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toContain('Plan unavailable')
  rerender(<FactionSummaries {...props} battle={{ ...battle, status: 'stopped' }} decisions={[{ ...decision, status: 'fallback', resolvedPlan: { name: 'Hold', rationale: 'Keep defending.' } }]} />)
  expect(screen.getByText('Hold').closest('summary')).toHaveTextContent(/^\s*fallback Hold$/)
  expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toContain('Keep defending.')
  expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toContain('Plan · fallback')
 })

 it('shows castle health and production chips in the faction color', () => {
  const castle: BattleBuilding = { id: 'a:castle', factionId: 'a', type: 'castle', position: { x: 0, y: 0 }, health: 161, maxHealth: 300, defense: 8, vision: 0, income: 10, trains: ['striker'], production: { unitType: 'striker', enabled: true } }
  render(<FactionSummaries {...props} battle={{ ...battle, participants: [...battle.participants, { id: 'b', name: 'Blue' }], state: { ...battle.state, buildings: [castle] } }} />)
  const amber = screen.getByRole('article', { name: 'Amber' })
  expect(within(amber).getByText('Castle').nextElementSibling).toHaveTextContent('161 / 300')
  expect(within(amber).getByRole('list', { name: 'Amber production' })).toBeInTheDocument()
  expect(within(screen.getByRole('article', { name: 'Blue' })).queryByRole('list')).not.toBeInTheDocument()
  expect(amber.style.getPropertyValue('--faction-color')).toBe('#69d2ff')
  expect(screen.getByRole('article', { name: 'Blue' }).style.getPropertyValue('--faction-color')).toBe('#ff6b55')
 })

 it('shows owned upgrades as an icon chip only for factions that have researched any', () => {
  const factions = [{ factionId: 'a', gold: 0, goldEarned: 0, goldSpent: 0, goldLooted: 0, ownedUpgradeIds: ['plating', 'plating_2', 'knighthood'] }, { factionId: 'b', gold: 0, goldEarned: 0, goldSpent: 0, goldLooted: 0 }]
  render(<FactionSummaries {...props} battle={{ ...battle, participants: [...battle.participants, { id: 'b', name: 'Blue' }], state: { ...battle.state, factions } }} />)
  const upgrades = within(screen.getByRole('article', { name: 'Amber' })).getByRole('group', { name: 'Amber upgrades' })
  expect(upgrades).toHaveAttribute('name', 'faction-a')
  expect(upgrades.querySelector('summary')).toHaveTextContent('Upgrades: Plating II: +4 defense for every unit; Knighthood (unlocks Heavy Cavalry): Stables can train Heavy Cavalry')
  expect(within(screen.getByRole('article', { name: 'Blue' })).queryByRole('group', { name: 'Blue upgrades' })).not.toBeInTheDocument()
 })

 it('puts activity and the applied plan name on the plan disclosure', () => {
  render(<FactionSummaries {...props} decisions={[{ ...decision, status: 'fallback', resolvedPlan: { name: 'Hold', rationale: 'Keep defending.' } }]} />)
  const summary = within(screen.getByRole('article', { name: 'Amber' })).getByText('Hold').closest('summary')
  expect(summary).toHaveTextContent('Fallback · fallback Hold')
  expect(summary).toHaveAttribute('title', 'Plan · fallback: Hold')
 })

 it('shows players the decision status and applied plan with its rationale', () => {
  render(<SessionContext.Provider value={{ enabled: true, authenticated: true, actor: { id: 'player', role: 'player' } }}>
    <FactionSummaries {...props} decisions={[{ ...decision, status: 'accepted', resolvedPlan: { name: 'Hold the bridge', rationale: 'Protect the economy.' } }]} />
  </SessionContext.Provider>)
  const summary = within(screen.getByRole('article', { name: 'Amber' })).getByText('Hold the bridge').closest('summary')
  expect(summary).toHaveTextContent('Plan ready · Hold the bridge')
  expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toContain('Protect the economy.')
 })
