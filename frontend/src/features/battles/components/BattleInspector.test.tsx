import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { BattleInspector } from './BattleInspector'
import type { BattleRun, DecisionHistory } from '../api/battles'

const battle: BattleRun = {
 id: 'battle', scenarioId: 'siege', scenarioDigest: 'digest', requestedTicks: 100, currentTick: 12, status: 'running', paused: false, attempt: 1, result: null,
 participants: [{ id: 'a', name: 'Amber', background: 'Saved origin story', doctrine: 'Protect the castle', controller: { type: 'model', modelIdentifier: 'model-a' } }, { id: 'b', name: 'Blue' }, { id: 'c', name: 'Crimson with a very long faction name' }],
 state: { battleId: 'battle', tick: 12, factions: ['a', 'b', 'c'].map((factionId) => ({ factionId, gold: 40, goldEarned: 10, goldSpent: 5, goldLooted: 0 })), units: null, buildings: null },
 map: { kind: 'authored', width: 1, height: 1, rows: ['.'], castleSpawns: [{ x: 0, y: 0 }, { x: 0, y: 0 }], economySites: [[], []] },
}
const decision: DecisionHistory = { requestId: 'a1', factionId: 'a', targetTick: 10, status: 'accepted', controllerType: 'model', usage: {}, createdAt: '', resolvedPlan: { name: 'Hold', rationale: 'Protect our opening economy' } }
const events = { battleAttempt: 1, selectedAttempt: 1, setSelectedAttempt: vi.fn(), events: [{ attempt: 1, sequence: 1, tick: 10, type: 'loot_collected', data: { amount: 5 }, createdAt: '' }], eventCursor: 1, eventHasMore: false, eventState: 'idle' as const, eventMessage: '', loadEventPage: vi.fn().mockResolvedValue(undefined) }
const props = { battle, decisions: [decision], decisionMessage: '', events }

describe('battle inspector navigation', () => {
 it('preserves System filters and inner tabs across three players', async () => {
  const user = userEvent.setup()
  render(<BattleInspector {...props} />)
  expect(screen.getByRole('combobox', { name: 'Inspect' })).toHaveValue('system')
  expect(within(screen.getByRole('combobox', { name: 'Inspect' })).getAllByRole('option')).toHaveLength(4)
  expect(screen.getByRole('table', { name: /Current player comparison/ })).toHaveTextContent('Amber')
  await user.selectOptions(screen.getByLabelText('Event type'), 'loot_collected')
  await user.selectOptions(screen.getByRole('combobox', { name: 'Inspect' }), 'a')
  await user.click(screen.getByRole('tab', { name: 'Units' }))
  expect(screen.getByRole('region', { name: 'Fielded units' })).toHaveTextContent('No units on the field')
  await user.selectOptions(screen.getByRole('combobox', { name: 'Inspect' }), 'c')
  expect(screen.getByRole('heading', { name: battle.participants[2].name })).toBeVisible()
  expect(screen.getByRole('tab', { name: 'Units' })).toHaveAttribute('aria-selected', 'true')
  await user.selectOptions(screen.getByRole('combobox', { name: 'Inspect' }), 'system')
  expect(screen.getByLabelText('Event type')).toHaveValue('loot_collected')
  await user.click(screen.getByRole('button', { name: 'Blue' }))
  expect(screen.getByRole('combobox', { name: 'Inspect' })).toHaveFocus()
  expect(screen.getByRole('combobox', { name: 'Inspect' })).toHaveValue('b')
 })

 it('shows saved background and legacy empty states without fetching live profiles', async () => {
  const fetch = vi.fn(); vi.stubGlobal('fetch', fetch)
  render(<BattleInspector {...props} />)
  await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Inspect' }), 'a')
  await userEvent.click(screen.getByRole('tab', { name: 'Faction' }))
  const profile = within(screen.getByRole('region', { name: 'Saved faction profile' }))
  expect(profile.getByText('Saved origin story')).toBeVisible()
  expect(profile.getByText('Protect the castle')).toBeVisible()
  await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Inspect' }), 'b')
  expect(screen.getByRole('region', { name: 'Saved faction profile' })).toHaveTextContent('No background saved with this battle.')
  expect(fetch).not.toHaveBeenCalled()
 })

 it('keeps an inspected decision expanded when newer decisions arrive and tabs change', async () => {
  const { rerender } = render(<BattleInspector {...props} />)
  await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Inspect' }), 'a')
  await userEvent.click(screen.getByRole('tab', { name: 'Decisions' }))
  expect(screen.getByText('Protect our opening economy')).toBeVisible()
  rerender(<BattleInspector {...props} decisions={[decision, { ...decision, requestId: 'a2', targetTick: 20, resolvedPlan: { name: 'Advance' } }]} />)
  expect(screen.getByText('Protect our opening economy')).toBeVisible()
  await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Inspect' }), 'system')
  await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Inspect' }), 'a')
  expect(screen.getByText('Protect our opening economy')).toBeVisible()
 })

 it('keeps the selector and inner tabs keyboard accessible', async () => {
  const user = userEvent.setup()
  render(<BattleInspector {...props} />)
  const selector = screen.getByRole('combobox', { name: 'Inspect' })
  await user.selectOptions(selector, 'a')
  selector.focus()
  await user.tab()
  expect(screen.getByRole('tab', { name: 'Overview' })).toHaveFocus()
  await user.keyboard('{ArrowRight}')
  expect(screen.getByRole('tab', { name: 'Decisions' })).toHaveFocus()
  await user.keyboard('{End}')
  expect(screen.getByRole('tab', { name: 'Faction' })).toHaveFocus()
  await user.keyboard('{Home}')
  expect(screen.getByRole('tab', { name: 'Overview' })).toHaveFocus()
  await user.keyboard('{ArrowLeft}')
  expect(screen.getByRole('tab', { name: 'Faction' })).toHaveFocus()
 })
})
