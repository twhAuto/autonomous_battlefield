import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CreateFactionPage } from './CreateFactionPage'

afterEach(() => vi.unstubAllGlobals())
describe('scenario-aware faction builder', () => {
 it('offers only selected content and submits a compatible blueprint', async () => {
  const rules = { id: 'rules', version: '1', designBudget: 120, units: [
   { id: 'ranger', cost: 30 }, { id: 'striker', cost: 25 },
  ], upgrades: [{ id: 'arsenal', cost: 20 }] }
  const fetcher = vi.fn(async (input: RequestInfo | URL) => new Response(JSON.stringify(String(input) === '/v1/scenarios' ? { scenarios: [{ id: 'ranged', rulesetId: 'rules', allowedUnits: ['ranger'], allowedUpgrades: [] }] } : { rulesets: [rules] }), { status: 200 }))
  vi.stubGlobal('fetch', fetcher)
  const user = userEvent.setup()
  render(<MemoryRouter><CreateFactionPage /></MemoryRouter>)
  await user.selectOptions(await screen.findByLabelText('Scenario compatibility'), 'ranged')
  expect(screen.queryByRole('button', { name: 'Add striker' })).not.toBeInTheDocument()
  expect(screen.queryByRole('checkbox', { name: /arsenal/i })).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Add ranger' })).toBeInTheDocument()
  await user.type(screen.getByLabelText('Faction name'), 'Ranged faction')
  await user.click(screen.getByRole('button', { name: /CREATE FACTION/i }))
  const calls = (fetcher.mock.calls as unknown as [string, RequestInit][])
  const create = calls.find(([url]) => url === '/v1/factions')
  expect(JSON.parse(create![1].body as string).blueprint).toEqual({ units: [{ type: 'ranger', count: 1 }], upgradeIds: [], recruitmentPriority: ['ranger'] })
  await user.selectOptions(screen.getByLabelText('Scenario compatibility'), '')
  expect(screen.getByRole('button', { name: 'Add striker' })).toBeInTheDocument()
 })
})
