import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { EventConsole } from './EventConsole'
import type { BattleEvent } from '../api/battles'

const events: BattleEvent[] = [
  { attempt: 1, sequence: 1, tick: 1, type: 'unit_recruited', data: { factionId: 'third', count: 2 }, createdAt: '' },
  { attempt: 1, sequence: 2, tick: 2, type: 'loot_collected', data: { factionId: 'first', amount: 10, nested: { value: true }, unknown: null }, createdAt: '' },
]
const props = { battleAttempt: 2, selectedAttempt: 1, setSelectedAttempt: vi.fn(), events, eventCursor: 2, eventHasMore: true, eventState: 'idle' as const, eventMessage: '', loadEventPage: vi.fn().mockResolvedValue(undefined), participantNames: new Map([['third', 'Crimson']]) }

describe('event inspector', () => {
  it('filters loaded events, resolves player names and exposes the complete payload', async () => {
    const user = userEvent.setup()
    render(<EventConsole {...props} />)
    expect(screen.getByLabelText('Event group')).toHaveValue('key')
    expect(screen.getAllByRole('listitem')).toHaveLength(1)
    await user.selectOptions(screen.getByLabelText('Event group'), 'all')
    expect(screen.getAllByRole('listitem')[0]).toHaveTextContent('loot collected')
    await user.selectOptions(screen.getByLabelText('Event type'), 'unit_recruited')
    expect(screen.getAllByRole('listitem')).toHaveLength(1)
    expect(screen.getByText('Crimson')).toHaveAttribute('title', 'third')
    await user.click(screen.getByText( 'Details · Inspect event #1'))
    expect(screen.getByText(/"factionId": "third"/)).toBeVisible()
    expect(screen.getByText('SHOWING 1 OF 2 LOADED EVENTS')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /LOAD NEXT/ }))
    expect(props.loadEventPage).toHaveBeenCalledWith(2, false)
    await user.selectOptions(screen.getByLabelText('Attempt'), '2')
    expect(props.setSelectedAttempt).toHaveBeenCalledWith(2)
  })

  it('keeps an active filter understandable when a new history contains no matches', async () => {
    const { rerender } = render(<EventConsole {...props} />)
    await userEvent.selectOptions(screen.getByLabelText('Event type'), 'unit_recruited')
    rerender(<EventConsole {...props} events={[events[1]]} eventHasMore={false} />)
    expect(screen.getByText('No matching events in the loaded history.')).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText('Event type'), 'all')
    expect(screen.getAllByRole('listitem')).toHaveLength(1)
    await userEvent.click(screen.getByRole('button', { name: 'REFRESH EVENTS' }))
    expect(props.loadEventPage).toHaveBeenCalledWith(2, false)
  })
})

it('keeps historical references readable and raw payloads exact across attempt changes', async () => {
  const historical: BattleEvent = { attempt: 1, sequence: 3, tick: 30, type: 'unit_attacked', createdAt: '', data: {
    attackerId: 'third:ranger:012', targetId: 'first:vanguard:003', sourceBuildingId: 'third:barracks:002',
    unitType: 'battering_ram', damage: 8, rationale: 'third:ranger:012 holds the line',
  } }
  const participantNames = new Map([['third', 'Crimson'], ['first', 'Amber']])
  const { rerender } = render(<EventConsole {...props} participantNames={participantNames} events={[historical]} />)
  await userEvent.selectOptions(screen.getByLabelText('Event group'), 'all')
  expect(screen.getByText('Crimson · Ranger #12')).toHaveAttribute('title', 'third:ranger:012')
  expect(screen.getByText('Amber · Vanguard #3')).toBeInTheDocument()
  expect(screen.getByText('Crimson · Barracks #2')).toBeInTheDocument()
  expect(screen.getByText('Battering Ram')).toBeInTheDocument()
  expect(screen.getByText('third:ranger:012 holds the line')).toBeInTheDocument()
  await userEvent.click(screen.getByText( 'Details · Inspect event #3'))
  expect(screen.getByText(/"attackerId":/)).toHaveTextContent('"attackerId": "third:ranger:012"')
  rerender(<EventConsole {...props} participantNames={participantNames} selectedAttempt={2} events={[{ ...historical, attempt: 2 }]} />)
  expect(screen.getByText('Crimson · Ranger #12')).toBeInTheDocument()
})

it('uses current entity metadata only for events from its matching attempt', () => {
  const id = 'b06d789f-43e7-4000-b000-f4de481ab075'
  const entities = new Map([[id, { id, type: 'herbalist', factionId: 'third' }]])
  const event: BattleEvent = { attempt: 2, sequence: 1, tick: 1, type: 'unit_destroyed', data: { unitId: id }, createdAt: '' }
  const { rerender } = render(<EventConsole {...props} selectedAttempt={2} events={[event]} entities={entities} />)
  expect(screen.getByText('Crimson · Herbalist · b06d789f')).toHaveAttribute('title', id)
  rerender(<EventConsole {...props} events={[{ ...event, attempt: 1 }]} entities={entities} />)
  expect(screen.getByText('Entity · b06d789f')).toBeInTheDocument()
})
