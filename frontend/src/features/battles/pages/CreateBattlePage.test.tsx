// Checks battle staging: setup loading and failure recovery, incomplete prerequisites,
// faction/scenario/controller validation, tick limits, AI availability rechecks, and submission.

import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import { CreateBattlePage } from './CreateBattlePage'
import { BattleApiError, createBattle, listModelProfiles, listScenarios, type ScenarioSummary } from '../api/battles'
import { FactionApiError, listFactions, type Faction } from '../../factions/api/factions'
import { SessionContext } from '../../auth/SessionContext'
import type { ModelProfile } from '../../../shared/domain/model-profile'

vi.mock('../api/battles', async (original) => ({
  ...await original<typeof import('../api/battles')>(),
  createBattle: vi.fn(), listScenarios: vi.fn(), listModelProfiles: vi.fn(),
}))
vi.mock('../../factions/api/factions', async (original) => ({
  ...await original<typeof import('../../factions/api/factions')>(),
  listFactions: vi.fn(),
}))

const battleID = '44444444-4444-4444-8444-444444444444'
const factions: Faction[] = [
  { id: 'north', name: 'North', doctrine: 'Hold the line.', background: '', ownerId: 'admin' },
  { id: 'south', name: 'South', doctrine: 'Advance.', background: '', ownerId: 'admin' },
]
const scenario: ScenarioSummary = {
  id: 'castle-siege-v1', rulesVersion: '1', rulesetId: 'castle-siege-v1',
  tickDuration: '1s', decisionIntervalTicks: 10, maximumTicks: 500, mapWidth: 15, mapHeight: 11,
}
const availableProfile: ModelProfile = { id: 'fast', displayName: 'Fast AI', available: true, runtime: 'api', modelLabel: 'fast-1' }
const offlineProfile: ModelProfile = { id: 'offline', displayName: 'Offline AI', available: false, runtime: 'api', modelLabel: '', unavailableReason: 'Disabled by operator' }

function LocationProbe() {
  return <output data-testid="location">{useLocation().pathname}</output>
}
// Both controller selects are labelled "Controller", so disambiguate by element id.
async function controllerSelect(id: 'first-controller' | 'second-controller') {
  const selects = await screen.findAllByRole('combobox', { name: 'Controller' })
  const select = selects.find((candidate) => candidate.id === id)
  if (!select) throw new Error(`no controller select ${id}`)
  return select
}
// The AI panel is a <details>; click its summary to expand it.
function integrationStatus() {
  return screen.getByText('AI integration status')
}
function renderPage(role: 'admin' | 'player' = 'admin') {
  return render(<MemoryRouter initialEntries={['/battles/new']}>
    <SessionContext.Provider value={{ enabled: true, authenticated: true, actor: { id: 'admin', role } }}>
      <LocationProbe />
      <Routes>
        <Route path="/battles/new" element={<CreateBattlePage />} />
        <Route path="/battles/:id" element={<h1>Battle staged</h1>} />
      </Routes>
    </SessionContext.Provider>
  </MemoryRouter>)
}

beforeEach(() => {
  vi.mocked(listFactions).mockReset().mockResolvedValue(factions)
  vi.mocked(listScenarios).mockReset().mockResolvedValue([scenario])
  vi.mocked(listModelProfiles).mockReset().mockResolvedValue([availableProfile, offlineProfile])
  vi.mocked(createBattle).mockReset().mockResolvedValue({ id: battleID, status: 'queued' })
})

it('shows the loading state, then prefills two distinct factions and the scenario', async () => {
  renderPage()
  expect(screen.getByText('Preparing the battlefield')).toBeInTheDocument()
  expect(await screen.findByRole('combobox', { name: 'Combat map' })).toHaveValue(scenario.id)
  expect(screen.getByRole('combobox', { name: 'Slot one' })).toHaveValue('north')
  expect(screen.getByRole('combobox', { name: 'Slot two' })).toHaveValue('south')
  expect(screen.getByRole('spinbutton', { name: 'Maximum ticks' })).toHaveValue(500)
  expect(screen.getByRole('button', { name: /DEPLOY COMBATANTS/ })).toBeEnabled()
  expect(screen.getByText('Rules v1 / decisions every 10 ticks / 1s tick pace')).toBeInTheDocument()
})

it('stages a battle with scripted controllers and navigates to the new battle', async () => {
  const user = userEvent.setup()
  renderPage()
  await user.click(await screen.findByRole('button', { name: /DEPLOY COMBATANTS/ }))
  await screen.findByRole('heading', { name: 'Battle staged' })
  expect(createBattle).toHaveBeenCalledWith({
    id: expect.any(String), scenarioId: scenario.id, tickCount: 500,
    participants: [
      { factionId: 'north', controller: { type: 'scripted' } },
      { factionId: 'south', controller: { type: 'scripted' } },
    ],
  })
  expect(screen.getByTestId('location')).toHaveTextContent(`/battles/${battleID}`)
})

it('submits a selected model controller as a model participant', async () => {
  const user = userEvent.setup()
  renderPage()
  await user.selectOptions(await controllerSelect('first-controller'), 'fast')
  await user.click(screen.getByRole('button', { name: /DEPLOY COMBATANTS/ }))
  await waitFor(() => expect(createBattle).toHaveBeenCalled())
  expect(vi.mocked(createBattle).mock.calls[0][0].participants[0].controller).toEqual({ type: 'model', profileId: 'fast' })
})

it('prevents selecting the same faction in both slots', async () => {
  renderPage()
  await screen.findByRole('combobox', { name: 'Slot two' })
  // Each slot disables the faction already chosen in the other, so a duplicate is unreachable.
  const inSlotTwo = screen.getByRole('combobox', { name: 'Slot two' })
  expect(screen.getByRole('option', { name: 'North', selected: true })).toBeInTheDocument()
  expect([...inSlotTwo.querySelectorAll('option')].find((option) => option.value === 'north')).toBeDisabled()
  expect([...screen.getByRole('combobox', { name: 'Slot one' }).querySelectorAll('option')].find((option) => option.value === 'south')).toBeDisabled()
})

it('offers only available controllers, keeping unavailable ones unselectable', async () => {
  vi.mocked(listModelProfiles).mockResolvedValue([availableProfile, offlineProfile])
  renderPage()
  const controller = await controllerSelect('first-controller')
  const values = [...controller.querySelectorAll('option')].map((option) => option.value)
  expect(values).toEqual(['scripted', 'fast'])
  expect(values).not.toContain('offline')
})

it('rejects a tick limit outside the scenario maximum', async () => {
  const user = userEvent.setup()
  renderPage()
  const ticks = await screen.findByRole('spinbutton', { name: 'Maximum ticks' })
  await user.clear(ticks)
  await user.type(ticks, '501')
  expect(screen.getByRole('alert')).toHaveTextContent('Enter a whole number from 1 to 500.')
  expect(ticks).toHaveAttribute('aria-invalid', 'true')
  expect(screen.getByRole('button', { name: /DEPLOY COMBATANTS/ })).toBeDisabled()
  await user.clear(ticks)
  expect(screen.getByRole('button', { name: /DEPLOY COMBATANTS/ })).toBeDisabled()
  await user.type(ticks, '250')
  expect(screen.getByRole('button', { name: /DEPLOY COMBATANTS/ })).toBeEnabled()
})

it('reports a failed setup load and recovers on retry', async () => {
  const user = userEvent.setup()
  vi.mocked(listScenarios).mockRejectedValueOnce(new BattleApiError('Scenario catalog unavailable', 503))
  renderPage()
  expect(await screen.findByRole('alert')).toHaveTextContent('Scenario catalog unavailable')
  await user.click(screen.getByRole('button', { name: 'RETRY CONNECTION' }))
  expect(await screen.findByRole('combobox', { name: 'Combat map' })).toHaveValue(scenario.id)
})

it('falls back to a generic message for unexpected load failures', async () => {
  vi.mocked(listFactions).mockRejectedValueOnce('offline')
  renderPage()
  expect(await screen.findByRole('alert')).toHaveTextContent('Deployment data could not be loaded.')
})

it('reports a faction API failure using its own message', async () => {
  vi.mocked(listFactions).mockRejectedValueOnce(new FactionApiError('Faction registry unreachable', 502))
  renderPage()
  expect(await screen.findByRole('alert')).toHaveTextContent('Faction registry unreachable')
})

it('directs the operator to create factions when fewer than two exist', async () => {
  vi.mocked(listFactions).mockResolvedValue([factions[0]])
  renderPage()
  expect(await screen.findByText('Two factions are required')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'CREATE A FACTION →' })).toHaveAttribute('href', '/factions/new')
})

it('explains an empty scenario catalog without offering a faction link', async () => {
  vi.mocked(listScenarios).mockResolvedValue([])
  renderPage()
  expect(await screen.findByText('No scenarios are available')).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: 'CREATE A FACTION →' })).not.toBeInTheDocument()
})

it('surfaces unavailable controllers and rechecks availability on demand', async () => {
  const user = userEvent.setup()
  renderPage()
  await screen.findByRole('button', { name: /DEPLOY COMBATANTS/ })
  await user.click(integrationStatus())
  expect(screen.getByText(/Disabled by operator/)).toBeInTheDocument()
  vi.mocked(listModelProfiles).mockResolvedValueOnce([{ ...offlineProfile, available: true, unavailableReason: '' }])
  await user.click(screen.getByRole('button', { name: 'Check availability' }))
  await waitFor(() => expect(screen.queryByText(/Disabled by operator/)).not.toBeInTheDocument())
})

it('reports when AI availability cannot be established and after a failed recheck', async () => {
  const user = userEvent.setup()
  vi.mocked(listModelProfiles).mockRejectedValueOnce(new Error('unreachable'))
  renderPage()
  await screen.findByRole('button', { name: /DEPLOY COMBATANTS/ })
  await user.click(integrationStatus())
  expect(screen.getByRole('alert')).toHaveTextContent('AI controller availability could not be established.')
  vi.mocked(listModelProfiles).mockRejectedValueOnce(new Error('still unreachable'))
  await user.click(screen.getByRole('button', { name: 'Check availability' }))
  await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('AI controller availability could not be established.'))
})

it('keeps the operator on the form when queuing the battle fails', async () => {
  const user = userEvent.setup()
  vi.mocked(createBattle).mockRejectedValueOnce(new BattleApiError('Scenario digest mismatch', 409))
  renderPage()
  await user.click(await screen.findByRole('button', { name: /DEPLOY COMBATANTS/ }))
  expect(await screen.findByText('Scenario digest mismatch')).toBeInTheDocument()
  expect(screen.getByTestId('location')).toHaveTextContent('/battles/new')
})

it('falls back to a generic message when queuing fails unexpectedly', async () => {
  const user = userEvent.setup()
  vi.mocked(createBattle).mockRejectedValueOnce('boom')
  renderPage()
  await user.click(await screen.findByRole('button', { name: /DEPLOY COMBATANTS/ }))
  expect(await screen.findByText('The battle could not be queued.')).toBeInTheDocument()
})

it('hides model provenance from players and shows it to inspectors', async () => {
  const user = userEvent.setup()
  const detailed: ModelProfile = { ...availableProfile, provider: 'vendor', scope: 'local' }
  vi.mocked(listModelProfiles).mockResolvedValue([detailed])
  const player = renderPage('player')
  const playerSelect = await controllerSelect('first-controller')
  await user.selectOptions(playerSelect, 'fast')
  expect(document.getElementById('first-controller-details')).toHaveTextContent('Fast AI')
  expect(screen.queryByText(/vendor/)).not.toBeInTheDocument()
  player.unmount()

  renderPage('admin')
  await user.selectOptions(await controllerSelect('first-controller'), 'fast')
  expect(screen.getByText('fast-1 · vendor · local')).toBeInTheDocument()
})

it('aborts in-flight setup reads when the page unmounts', async () => {
  vi.mocked(listFactions).mockImplementation(() => new Promise(() => {}))
  const view = renderPage()
  const signal = vi.mocked(listFactions).mock.calls[0][0]
  view.unmount()
  expect(signal?.aborted).toBe(true)
})

it('explains three-level costs when staging every map', async () => {
  vi.mocked(listScenarios).mockResolvedValue([{ ...scenario, id: 'frontier-siege', rulesetId: 'battlefield', rulesVersion: '3' }])
  renderPage()
  expect(await screen.findByText(/Three upgrade levels: each costs 20, 50, then 100 gold/)).toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: 'Combat map' })).toHaveValue('frontier-siege')
})
