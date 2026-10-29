// Checks battle staging: setup loading and failure recovery, incomplete prerequisites,
// faction/scenario/controller validation, tick limits, timing overrides, controller availability, and submission.

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
  expect(screen.getByRole('combobox', { name: 'Tick duration' })).toHaveValue('')
  expect(screen.getByRole('combobox', { name: 'Decision interval' })).toHaveValue('')
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

it('allows scripted battles when AI availability fails without showing integration diagnostics', async () => {
  vi.mocked(listModelProfiles).mockRejectedValueOnce(new Error('unreachable'))
  renderPage()
  expect(await screen.findByRole('button', { name: /DEPLOY COMBATANTS/ })).toBeEnabled()
  expect(screen.queryByText('AI integration status')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Check availability' })).not.toBeInTheDocument()
  expect((await controllerSelect('first-controller')).querySelectorAll('option')).toHaveLength(1)
  expect(screen.getByRole('alert')).toHaveTextContent('AI controllers could not be loaded.')
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

it('keeps map selection free of roster and rules explanations', async () => {
  vi.mocked(listScenarios).mockResolvedValue([{ ...scenario, id: 'frontier-siege', allowedUnits: ['vanguard'], allowedBuildings: ['castle'] }])
  renderPage()
  expect(await screen.findByRole('combobox', { name: 'Combat map' })).toHaveValue('frontier-siege')
  expect(screen.queryByText(/Units:|Buildings:|Three upgrade levels/)).not.toBeInTheDocument()
})

it('submits timing overrides and resets them to the new map defaults when maps change', async () => {
  vi.mocked(listScenarios).mockResolvedValue([scenario, { ...scenario, id: 'other', tickDuration: '2s', decisionIntervalTicks: 20 }])
  const user = userEvent.setup()
  renderPage()
  await user.selectOptions(await screen.findByLabelText('Tick duration'), '500ms')
  await user.selectOptions(screen.getByLabelText('Decision interval'), '5')
  await user.selectOptions(screen.getByLabelText('Combat map'), 'other')
  expect(screen.getByLabelText('Tick duration')).toHaveValue('')
  expect(screen.getByLabelText('Decision interval')).toHaveValue('')
  expect(screen.getByRole('option', { name: 'Map default (2s)' })).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'Map default (20 ticks)' })).toBeInTheDocument()
  await user.selectOptions(screen.getByLabelText('Tick duration'), '250ms')
  await user.selectOptions(screen.getByLabelText('Decision interval'), '1')
  await user.click(screen.getByRole('button', { name: /DEPLOY COMBATANTS/ }))
  await waitFor(() => expect(createBattle).toHaveBeenCalledWith(expect.objectContaining({ scenarioId: 'other', tickDuration: '250ms', decisionIntervalTicks: 1 })))
})

it('defaults effort to low, and to the first supported effort when a model lacks low', async () => {
  vi.mocked(listModelProfiles).mockResolvedValue([{ ...availableProfile, runtime: 'cli', modelOptions: [
    { id: 'fast-1', reasoningEfforts: ['low', 'high'] }, { id: 'deep', reasoningEfforts: ['medium', 'high'] },
  ] }])
  const user = userEvent.setup()
  renderPage()
  await user.selectOptions(await controllerSelect('first-controller'), 'fast')
  expect(screen.getByRole('combobox', { name: 'Reasoning effort' })).toHaveValue('low')
  expect(screen.queryByRole('option', { name: 'Model default' })).not.toBeInTheDocument()
  await user.selectOptions(screen.getByRole('combobox', { name: 'Reasoning effort' }), 'high')
  await user.selectOptions(screen.getByRole('combobox', { name: 'Model' }), 'deep')
  expect(screen.getByRole('combobox', { name: 'Reasoning effort' })).toHaveValue('medium')
  expect(screen.queryByRole('option', { name: 'Low' })).not.toBeInTheDocument()
  await user.selectOptions(screen.getByRole('combobox', { name: 'Model' }), 'fast-1')
  await user.click(screen.getByRole('button', { name: /DEPLOY COMBATANTS/ }))
  await screen.findByRole('heading', { name: 'Battle staged' })
  expect(createBattle).toHaveBeenCalledWith(expect.objectContaining({ participants: [
    { factionId: 'north', controller: { type: 'model', profileId: 'fast', modelIdentifier: 'fast-1', reasoningEffort: 'low' } },
    { factionId: 'south', controller: { type: 'scripted' } },
  ] }))
})

it('submits a chosen effort for the configured model', async () => {
  vi.mocked(listModelProfiles).mockResolvedValue([{ ...availableProfile, modelOptions: [{ id: 'fast-1', reasoningEfforts: ['low', 'high'] }] }])
  const user = userEvent.setup()
  renderPage()
  await user.selectOptions(await controllerSelect('second-controller'), 'fast')
  await user.selectOptions(screen.getByRole('combobox', { name: 'Reasoning effort' }), 'high')
  await user.click(screen.getByRole('button', { name: /DEPLOY COMBATANTS/ }))
  await screen.findByRole('heading', { name: 'Battle staged' })
  expect(createBattle).toHaveBeenCalledWith(expect.objectContaining({ participants: [
    { factionId: 'north', controller: { type: 'scripted' } },
    { factionId: 'south', controller: { type: 'model', profileId: 'fast', modelIdentifier: 'fast-1', reasoningEffort: 'high' } },
  ] }))
})

it('defaults to frontier-siege and previews the selected map layout', async () => {
  const map = { kind: 'authored' as const, width: 3, height: 2, rows: ['.g#', 'f..'], castleSpawns: [{ x: 0, y: 0 }, { x: 2, y: 1 }] as [{ x: number; y: number }, { x: number; y: number }], economySites: [[], []] as [[], []] }
  vi.mocked(listScenarios).mockResolvedValue([scenario, { ...scenario, id: 'frontier-siege', mapWidth: 3, mapHeight: 2, map }])
  const user = userEvent.setup()
  renderPage()
  expect(await screen.findByRole('combobox', { name: 'Combat map' })).toHaveValue('frontier-siege')
  const preview = screen.getByTitle('frontier-siege layout, 3 by 2 hexes').closest('svg')!
  expect(preview.querySelectorAll('.hex-tile')).toHaveLength(6)
  expect(preview.querySelector('.hex-tile--gold')).not.toBeNull()
  expect(screen.queryByText(/gold mines ·/)).not.toBeInTheDocument()
  expect(screen.getByText('Slot 1 castle · (0, 0)')).toBeInTheDocument()
  expect(screen.getByText('Slot 2 castle · (2, 1)')).toBeInTheDocument()
  await user.selectOptions(screen.getByLabelText('Combat map'), 'castle-siege-v1')
  expect(screen.queryByTitle(/layout/)).not.toBeInTheDocument()
})

it('summarizes a map\'s gold mines in the preview', async () => {
  const map = { kind: 'authored' as const, width: 3, height: 1, rows: ['g.g'], goldMineReserve: 800, castleSpawns: [{ x: 0, y: 0 }, { x: 2, y: 0 }] as [{ x: number; y: number }, { x: number; y: number }], economySites: [[], []] as [[], []] }
  vi.mocked(listScenarios).mockResolvedValue([{ ...scenario, id: 'frontier-siege', mapWidth: 3, mapHeight: 1, map }])
  renderPage()
  expect(await screen.findByText('2 gold mines · 800 gold each')).toBeInTheDocument()
  expect(screen.getAllByText('Gold mine: 800 of 800 gold left; economy buildings only, none beside it · (0, 0)')).toHaveLength(1)
})

it('colors castle spawns by sorted faction ID like the live map', async () => {
  const map = { kind: 'authored' as const, width: 2, height: 1, rows: ['..'], castleSpawns: [{ x: 0, y: 0 }, { x: 1, y: 0 }] as [{ x: number; y: number }, { x: number; y: number }], economySites: [[], []] as [[], []] }
  vi.mocked(listFactions).mockResolvedValue([factions[1], factions[0]])
  vi.mocked(listScenarios).mockResolvedValue([{ ...scenario, map }])
  renderPage()
  const castles = (await screen.findByTitle(/layout/)).closest('svg')!.querySelectorAll<SVGGElement>('.map-preview__castle')
  expect(castles[0].style.stroke).toBe('rgb(255, 107, 85)')
  expect(castles[1].style.stroke).toBe('rgb(105, 210, 255)')
})

it('clears model settings when switching controller and disables unsupported effort', async () => {
  const user = userEvent.setup()
  renderPage()
  await user.selectOptions(await controllerSelect('first-controller'), 'fast')
  expect(screen.getByRole('combobox', { name: 'Reasoning effort' })).toBeDisabled()
  await user.selectOptions(await controllerSelect('first-controller'), 'scripted')
  expect(screen.queryByRole('combobox', { name: 'Model' })).not.toBeInTheDocument()
})
