import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { DecisionContextPanel } from './DecisionContextPanel'
import type { SavedDecisionContext } from '../api/battles'

const saved: SavedDecisionContext = { requestId: 'request', battleId: 'battle', factionId: 'a', attempt: 2, targetTick: 10, observationTick: 8, context: {
 opening: false, factionName: 'Amber', ownedUpgradeIds: null,
 economy: { gold: 25, projectedIncome: 10, economyBuildingCost: 50, economyBuildingIncome: 10, currentEconomyBuildings: 1, maximumEconomyBuildings: 3 },
 view: { map: { kind: 'authored', width: 1, height: 1, rows: ['.'], castleSpawns: [{ x: 0, y: 0 }, { x: 0, y: 0 }], economySites: [[], []] }, state: { battleId: 'battle', tick: 8, factions: null, units: null, buildings: null }, visiblePositions: [] },
 legal: { actionKinds: ['attack', 'defend'], units: [{ id: 'ranger', cost: 35 }], economySites: null }, activePlan: { name: 'Prior plan' },
} }
const response = (data = saved) => new Response(JSON.stringify(data), { status: 200 })
const makeProps = () => ({ battleId: 'battle', requestId: 'request', attempt: 2, active: true, cache: new Map<string, SavedDecisionContext>() })

beforeEach(() => vi.stubGlobal('fetch', vi.fn()))
describe('saved context loading', () => {
 it('loads only after expansion, displays historical values, and reuses scoped cache', async () => {
  vi.mocked(fetch).mockResolvedValue(response())
  const props = makeProps()
  const { rerender } = render(<DecisionContextPanel {...props} />)
  expect(fetch).not.toHaveBeenCalled()
  await userEvent.click(screen.getByText('Saved decision context'))
  expect(await screen.findByText('What this AI knew at tick 8')).toBeVisible()
  expect(screen.getByText('Gold then').nextElementSibling).toHaveTextContent('25')
  expect(screen.getByText('Ranger (35 gold)')).toBeVisible()
  expect(fetch).toHaveBeenCalledWith('/v1/battles/battle/decisions/request/context?attempt=2', expect.objectContaining({ signal: expect.any(AbortSignal) }))
  await userEvent.click(screen.getByText('Prior active plan'))
  expect(screen.getByText(/"name": "Prior plan"/)).toBeVisible()
  rerender(<DecisionContextPanel {...props} active={false} />)
  rerender(<DecisionContextPanel {...props} />)
  expect(fetch).toHaveBeenCalledTimes(1)
 })

 it('keeps failures local, supports retry, and handles missing legacy observations', async () => {
  vi.mocked(fetch).mockResolvedValueOnce(new Response('error', { status: 500 })).mockResolvedValueOnce(response({ ...saved, context: null }))
  render(<DecisionContextPanel {...makeProps()} />)
  await userEvent.click(screen.getByText('Saved decision context'))
  expect(await screen.findByRole('alert')).toHaveTextContent('Decision context could not be loaded.')
  await userEvent.click(screen.getByRole('button', { name: 'Retry context' }))
  expect(await screen.findByText('No observation was saved for this decision.')).toBeVisible()
 })

 it('aborts and ignores a late response after leaving the selected player', async () => {
  let finish!: (response: Response) => void
  vi.mocked(fetch).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve })).mockResolvedValueOnce(response())
  const props = makeProps()
  const { rerender } = render(<DecisionContextPanel {...props} />)
  await userEvent.click(screen.getByText('Saved decision context'))
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1))
  const signal = vi.mocked(fetch).mock.calls[0][1]?.signal
  rerender(<DecisionContextPanel {...props} active={false} />)
  expect(signal?.aborted).toBe(true)
  await act(async () => finish(response({ ...saved, observationTick: 999 })))
  expect(props.cache.size).toBe(0)
  expect(screen.queryByText('What this AI knew at tick 999')).not.toBeInTheDocument()
  rerender(<DecisionContextPanel {...props} />)
  expect(await screen.findByText('What this AI knew at tick 8')).toBeVisible()
 })

 it('reports an unavailable context endpoint without crashing', async () => {
  vi.mocked(fetch).mockResolvedValue(new Response('', { status: 404 }))
  render(<DecisionContextPanel {...makeProps()} />)
  await userEvent.click(screen.getByText('Saved decision context'))
  expect(await screen.findByRole('alert')).toHaveTextContent('Saved context is unavailable for this decision.')
 })
})

 it('separates compact initial context from optional unit details', async () => {
  const compact = { ...saved, initialContext: { legal: { units: [{ id: 'ranger', cost: 10, role: 'ranged' }] } }, availableUnitDetails: { units: [{ unitId: 'ranger', stats: { attack: 20 } }] } }
  vi.mocked(fetch).mockResolvedValueOnce(response(compact))
  render(<DecisionContextPanel battleId="battle" requestId="request" attempt={2} active cache={new Map()} />)
  await userEvent.click(screen.getByText('Saved decision context'))
  expect(await screen.findByText('Reconstructed agent context')).toBeInTheDocument()
  expect(screen.getByText('Unit details available on demand')).toBeInTheDocument()
 })
