import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { HexBattleMap } from './HexBattleMap'
import type { BattleMap } from '../api/battles'

const map: BattleMap = {
  kind: 'authored' as const, width: 2, height: 2, rows: ['..', 'b#'],
  castleSpawns: [{ x: 0, y: 0 }, { x: 1, y: 0 }],
  economySites: [[{ x: 0, y: 1 }], []],
}
const state = {
  battleId: 'battle', tick: 3,
  factions: [{ factionId: 'a', gold: 40, goldEarned: 20, goldSpent: 50, goldLooted: 0 }],
  units: [
    { id: 'a:ranger:1', factionId: 'a', type: 'ranger', position: { x: 0, y: 1 }, health: 20, maxHealth: 30, attack: 11, defense: 2, speed: 6, movement: 1, range: 3, vision: 4, readyAtTick: 0, movedFrom: { x: 1, y: 1 }, attackTargetId: 'a:castle', attackTargetPosition: { x: 0, y: 0 } },
    { id: 'a:striker:dead', factionId: 'a', type: 'striker', position: { x: 1, y: 0 }, health: 0, maxHealth: 42, attack: 14, defense: 3, speed: 8, movement: 2, range: 1, vision: 3, readyAtTick: 0 },
  ],
  buildings: [{ id: 'a:castle', factionId: 'a', type: 'castle' as const, position: { x: 0, y: 0 }, health: 250, maxHealth: 300, defense: 8, vision: 4, income: 10 }],
}

describe('HexBattleMap', () => {
  it('renders terrain, accessible entities, selection, and map controls', async () => {
    const user = userEvent.setup()
    const { container } = render(<HexBattleMap map={map} state={state} visiblePositions={[{ x: 0, y: 0 }, { x: 0, y: 1 }]} factionNames={new Map([['a', 'Northwind']])} toolbarControls={<label htmlFor="test-perspective">Perspective<select id="test-perspective"><option>Spectator</option></select></label>} />)
    const battlefield = container.querySelector('svg')!
    expect(battlefield).toHaveAccessibleName(/2 by 2 pointy-top hex battlefield/i)
    expect(container.querySelectorAll('.hex-tile')).toHaveLength(4)
    expect(container.querySelectorAll('.hex-tile--fog')).toHaveLength(2)
    expect(container.querySelectorAll('.unit-direction--movement')).toHaveLength(1)
    expect(container.querySelectorAll('.unit-direction--attack')).toHaveLength(1)
    expect(screen.getByText(/a:ranger:1 attacked a:castle/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'striker a:striker:dead' })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Visible entity summary' })).not.toBeInTheDocument()
    expect(container.querySelector('.map-toolbar')).toContainElement(screen.getByLabelText('Perspective'))
    await user.click(screen.getByRole('button', { name: 'ranger a:ranger:1' }))
    expect(screen.getByText(/HP 20\/30/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Zoom in' }))
    await user.click(screen.getByRole('button', { name: 'Zoom out' }))
    await user.click(screen.getByRole('button', { name: 'RESET' }))
    const layer = container.querySelector('.map-layer')!
    const transformBeforeScroll = layer.getAttribute('transform')
    fireEvent.wheel(battlefield, { deltaY: -1 })
    expect(layer).toHaveAttribute('transform', transformBeforeScroll)
    fireEvent.pointerDown(battlefield, { pointerId: 1, clientX: 10, clientY: 10 })
    fireEvent.pointerMove(battlefield, { pointerId: 1, clientX: 20, clientY: 24 })
    fireEvent.pointerUp(battlefield, { pointerId: 1 })
    fireEvent.pointerCancel(battlefield, { pointerId: 1 })
    fireEvent.keyDown(screen.getByRole('button', { name: 'castle a:castle' }), { key: 'Enter' })
    expect(screen.getByText(/HP 250\/300/)).toBeInTheDocument()
  })
})
