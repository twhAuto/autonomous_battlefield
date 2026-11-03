import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import type { AnalyticsReport } from '../api/analytics'
import { AnalyticsPage } from './AnalyticsPage'

const sonnet = { type: 'model' as const, profileId: 'claude', modelIdentifier: 'sonnet', reasoningEffort: 'low' }
const scripted = { type: 'scripted' as const }
const outcome = { battles: 4, wins: 3, draws: 0, losses: 1, castleWins: 2, winRate: 0.75, winRateLow: 0.3, winRateHigh: 0.95, meanTicksToWin: 210 }

function report(change: Partial<AnalyticsReport> = {}): AnalyticsReport {
  return {
    rulesVersion: '9', truncated: false, excludedForFallback: 0, battles: 4, battlesWithFallback: 1, sidesWithUnitStats: 8,
    models: [
      { ...outcome, controller: sonnet, decisions: 40, fallbackRate: 0.05, invalidPlanRate: 0.1, meanDecisionLatencyMs: 12500, tokensPerBattle: { input: 52000, cachedInput: 9000, output: 4100 } },
      { ...outcome, wins: 1, losses: 3, winRate: 0.25, winRateLow: 0.05, winRateHigh: 0.7, meanTicksToWin: null, controller: scripted, decisions: 0, fallbackRate: 0, invalidPlanRate: 0, meanDecisionLatencyMs: null, tokensPerBattle: { input: 0, cachedInput: 0, output: 0 } },
    ],
    factions: [{ ...outcome, factionId: 'f1', factionName: 'Amber' }],
    headToHead: [{ controller: sonnet, opponent: scripted, battles: 4, wins: 3, draws: 0, losses: 1 }, { controller: scripted, opponent: sonnet, battles: 4, wins: 1, draws: 0, losses: 3 }],
    units: [{ unitType: 'heavy_cavalry', fielded: 10, recruited: 8, lost: 4, kills: 12, buildingsDestroyed: 1, damageDealt: 900, damageTaken: 500, healthRestored: 0, goldSpent: 450, killsPerLoss: 3, damagePerGold: 2, shareInWins: 0.4, shareInLosses: 0.1 }],
    unitsByModel: [{ controller: sonnet, unitType: 'heavy_cavalry', recruited: 8, armyShare: 0.5 }],
    upgrades: [{ upgradeId: 'knighthood', purchases: 3, purchaseRate: 0.375, meanPurchaseTick: 180, winRateWhenPurchased: 0.66, winRateWhenNot: null }],
    maps: [{ scenarioId: 'frontier-siege', battles: 4, draws: 1, drawRate: 0.25, meanTicks: 320, sides: [{ wins: 2, winRate: 0.5 }, { wins: 1, winRate: 0.25 }] }],
    ...change,
  }
}

const reply = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })
let lastSearch = ''
function Location() {
  lastSearch = useLocation().search
  return null
}

function serve(body: () => Response = () => reply(200, report())) {
  vi.mocked(fetch).mockImplementation(async (input) => String(input).includes('/v1/leagues')
    ? reply(200, { leagues: [{ id: 'league-1', name: 'Autumn cup' }] })
    : body())
}

function show(path = '/analytics') {
  return render(<MemoryRouter initialEntries={[path]}><Routes><Route path="/analytics" element={<><AnalyticsPage /><Location /></>} /></Routes></MemoryRouter>)
}

beforeEach(() => { vi.stubGlobal('fetch', vi.fn()) })

it('shows the leaderboard and every table', async () => {
  serve()
  show()
  expect(await screen.findByText('Win rate by controller')).toBeInTheDocument()
  const chartRows = within(screen.getByRole('figure')).getAllByRole('button')
  expect(chartRows[0]).toHaveAccessibleName('sonnet · low: 75% won of 4 battles, 95% interval 30% to 95%')
  fireEvent.focus(chartRows[0])
  expect(within(chartRows[0]).getByRole('tooltip')).toHaveTextContent('3W 0D 1L')
  fireEvent.blur(chartRows[0])
  expect(within(chartRows[0]).queryByRole('tooltip')).not.toBeInTheDocument()
  fireEvent.pointerEnter(chartRows[1])
  expect(within(chartRows[1]).getByRole('tooltip')).toHaveTextContent('25%')
  fireEvent.pointerLeave(chartRows[1])
  expect(within(chartRows[1]).queryByRole('tooltip')).not.toBeInTheDocument()
  // A tap shows the record where there is no hover.
  fireEvent.click(chartRows[1])
  expect(within(chartRows[1]).getByRole('tooltip')).toHaveTextContent('1W 0D 3L')
  const models = screen.getByRole('table', { name: 'Controller records and decision statistics' })
  expect(models).toHaveTextContent('12.5 s')
  expect(models).toHaveTextContent('52K · 9K · 4.1K')
  expect(screen.getByRole('table', { name: "Each controller's record against each other controller" })).toHaveTextContent('3–0–1')
  expect(screen.getByRole('table', { name: 'Unit statistics' })).toHaveTextContent('heavy cavalry')
  expect(screen.getByRole('table', { name: "Army mix: each controller's recruits by unit type" })).toHaveTextContent('50%')
  expect(screen.getByRole('table', { name: 'Upgrade statistics' })).toHaveTextContent('66%')
  expect(screen.getByRole('table', { name: 'Faction records' })).toHaveTextContent('Amber')
  expect(screen.getByRole('table', { name: 'Map balance' })).toHaveTextContent('frontier-siege')
})

it('keeps filters in the URL and refetches with them', async () => {
  serve()
  const user = userEvent.setup()
  show()
  await screen.findByText('Win rate by controller')
  await user.selectOptions(await screen.findByLabelText('Battles'), 'league-1')
  await user.click(screen.getByRole('checkbox', { name: 'Every rules version' }))
  await user.click(screen.getByRole('checkbox', { name: 'Leave out battles with scripted fallbacks' }))
  expect(lastSearch).toBe('?leagueId=league-1&rules=all&fallback=exclude')
  const calls = vi.mocked(fetch).mock.calls.map(([input]) => String(input)).filter(url => url.includes('/v1/analytics'))
  expect(calls.at(-1)).toContain('leagueId=league-1&rulesVersion=all&excludeFallback=true')
  await user.selectOptions(screen.getByLabelText('Battles'), '')
  expect(lastSearch).toBe('?rules=all&fallback=exclude')
})

it('links back to the selected league', async () => {
  serve()
  const user = userEvent.setup()
  show('/analytics?leagueId=league-1')
  await screen.findByText('Win rate by controller')
  expect(screen.getByRole('link', { name: '← BACK TO LEAGUE' })).toHaveAttribute('href', '/leagues/league-1')
  expect(screen.getByRole('link', { name: '← MAIN MENU' })).toHaveAttribute('href', '/')
  await user.selectOptions(screen.getByLabelText('Battles'), '')
  expect(screen.queryByRole('link', { name: '← BACK TO LEAGUE' })).not.toBeInTheDocument()
})

it('explains empty, truncated and failed reports', async () => {
  serve(() => reply(200, report({ battles: 0 })))
  const view = show('/analytics?leagueId=gone')
  expect(await screen.findByRole('heading', { name: 'No completed battles match' })).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'Selected league' })).toBeInTheDocument()
  view.unmount()
  serve(() => reply(200, report({ truncated: true, rulesVersion: null, headToHead: [], units: [], upgrades: [] })))
  const second = show()
  expect(await screen.findByText(/Only the 5,000 most recent battles/)).toBeInTheDocument()
  expect(screen.getByText('All')).toBeInTheDocument()
  expect(screen.queryByRole('table', { name: 'Unit statistics' })).not.toBeInTheDocument()
  second.unmount()
  serve(() => reply(403, {}))
  show()
  expect(await screen.findByRole('alert')).toHaveTextContent('cannot view statistics')
})

it('drops the previous report when a filter change fails', async () => {
  let fail = false
  serve(() => fail ? reply(500, {}) : reply(200, report()))
  const user = userEvent.setup()
  show()
  expect(await screen.findByRole('table', { name: 'Faction records' })).toBeInTheDocument()
  fail = true
  await user.click(screen.getByRole('checkbox', { name: 'Every rules version' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Statistics unavailable')
  expect(screen.queryByRole('table', { name: 'Faction records' })).not.toBeInTheDocument()
  expect(screen.queryByText('Win rate by controller')).not.toBeInTheDocument()
})
