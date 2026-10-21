import { act, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { DecisionTiming } from './DecisionTiming'
import type { DecisionHistory } from '../api/battles'

const start = '2026-10-21T00:00:00.000Z'
const decision: DecisionHistory = { requestId: 'r', factionId: 'a', targetTick: 10, status: 'pending', controllerType: 'model', usage: {}, createdAt: start }
afterEach(() => vi.useRealTimers())

it('updates queued and running waits and stops on terminal resolution', () => {
  vi.useFakeTimers(); vi.setSystemTime(new Date(start))
  const view = render(<DecisionTiming decision={decision} active />)
  act(() => vi.advanceTimersByTime(3000))
  expect(screen.getByText('Queued · 3.0s')).toBeInTheDocument()
  view.rerender(<DecisionTiming decision={{ ...decision, status: 'running', claimedAt: '2026-10-21T00:00:02Z' }} active />)
  act(() => vi.advanceTimersByTime(2000))
  expect(screen.getByText('Deciding · 5.0s')).toBeInTheDocument()
  view.rerender(<DecisionTiming decision={{ ...decision, status: 'accepted', resolvedAt: '2026-10-21T00:00:04Z', runnerCompletedAt: '2026-10-21T00:00:06Z', cliProcessStartMs: 2 }} active />)
  act(() => vi.advanceTimersByTime(5000))
  expect(screen.getByText('Plan accepted in · 4.0s')).toBeInTheDocument()
  expect(screen.getByText('2.0s')).toBeInTheDocument()
  expect(screen.getByText('2ms')).toBeInTheDocument()
  expect(vi.getTimerCount()).toBe(0)
})

it('labels fallback separately and preserves unknown historical measurements', () => {
  const view = render(<DecisionTiming decision={{ ...decision, status: 'fallback', resolvedAt: '2026-10-21T00:02:00Z' }} active={false} />)
  expect(screen.getByText('Resolved in · 120.0s')).toBeInTheDocument()
  expect(screen.getAllByText('Not recorded').length).toBeGreaterThan(0)
  view.rerender(<DecisionTiming decision={{ ...decision, createdAt: 'bad', status: 'failed' }} active={false} />)
  expect(screen.getByText('Resolved in · Not recorded')).toBeInTheDocument()
  view.rerender(<DecisionTiming decision={decision} active={false} />)
  expect(screen.getByText('Queued · Not recorded')).toBeInTheDocument()
})
