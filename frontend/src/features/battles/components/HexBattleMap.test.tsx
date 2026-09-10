// Checks hex-map terrain, accessible entity details, selection, and map controls.

import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
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
    expect(screen.getByText(/Northwind · Ranger #1 attacked Northwind · Castle/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Northwind · Striker · a:strike' })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Visible entity summary' })).not.toBeInTheDocument()
    expect(container.querySelector('.map-toolbar')).toContainElement(screen.getByLabelText('Perspective'))
    await user.click(screen.getByRole('button', { name: 'Northwind · Ranger #1' }))
    expect(screen.getByText(/HP 20\/30/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Zoom in' }))
    await user.click(screen.getByRole('button', { name: 'Zoom out' }))
    await user.click(screen.getByRole('button', { name: 'Fit map' }))
    const layer = container.querySelector('.map-layer')!
    const transformBeforeScroll = layer.getAttribute('transform')
    fireEvent.wheel(battlefield, { deltaY: -1 })
    expect(layer).toHaveAttribute('transform', transformBeforeScroll)
    fireEvent.pointerDown(battlefield, { pointerId: 1, clientX: 10, clientY: 10 })
    fireEvent.pointerMove(battlefield, { pointerId: 1, clientX: 20, clientY: 24 })
    fireEvent.pointerUp(battlefield, { pointerId: 1 })
    fireEvent.pointerCancel(battlefield, { pointerId: 1 })
    fireEvent.keyDown(screen.getByRole('button', { name: 'Northwind · Castle' }), { key: 'Enter' })
    expect(screen.getByText(/HP 250\/300/)).toBeInTheDocument()
  })
})

it('renders distinct tactical artwork and readable labels, with separate keyboard selection', async () => {
  const unitTypes = ['vanguard', 'striker', 'ranger', 'catapult', 'battering_ram', 'pikeman', 'herbalist', 'new_scout']
  const buildingTypes = ['castle', 'economy', 'defense_tower', 'watchtower', 'barracks'] as const
  const snapshot = { ...state,
    units: unitTypes.map((type, index) => ({ ...state.units[0], id: `a:${type}:${index + 1}`, type, movedFrom: undefined, attackTargetPosition: undefined })),
    buildings: buildingTypes.map((type, index) => ({ ...state.buildings[0], id: type === 'castle' ? 'a:castle' : `a:${type}:${index + 1}`, type })),
  }
  const { container, rerender } = render(<HexBattleMap map={map} state={snapshot} visiblePositions={[]} factionNames={new Map([['a', 'Northwind']])} />)
  const markers = container.querySelectorAll('.map-entity')
  expect(markers).toHaveLength(13)
  expect(new Set([...markers].map((marker) => marker.querySelector('.entity-glyph path')?.getAttribute('d'))).size).toBe(13)
  expect(container.querySelectorAll('.map-entity text')).toHaveLength(0)
  const scout = screen.getByRole('button', { name: 'Northwind · New Scout #8' })
  expect(scout).toHaveAttribute('aria-pressed', 'false')
  expect(scout.querySelector('title')).toHaveTextContent('a:new_scout:8')
  scout.focus()
  await userEvent.keyboard(' ')
  expect(scout).toHaveFocus()
  expect(scout).toHaveAttribute('aria-pressed', 'true')
  expect(container.querySelector('.entity-details')).toHaveTextContent('Northwind · New Scout #8')
  await userEvent.click(screen.getByText('Unit & building icons'))
  expect(screen.getByText('Battering Ram', { exact: true })).toBeVisible()
  expect(screen.getByText('Defense Tower', { exact: true })).toBeVisible()
  expect(screen.getByText('New Scout', { exact: true })).toBeVisible()
  // Polling reorders living units and changes HP without changing selection/names.
  rerender(<HexBattleMap map={map} state={{ ...snapshot, units: [...snapshot.units].reverse().map((unit) => ({ ...unit, health: 10 })) }} visiblePositions={[]} factionNames={new Map([['a', 'Northwind']])} />)
  expect(screen.getByRole('button', { name: 'Northwind · New Scout #8' })).toHaveAttribute('aria-pressed', 'true')
  expect(container.querySelector('.entity-details')).toHaveTextContent('HP 10/30')
})


it('clears adjacent icons and removes decorative tracers when actions end', () => {
  const { container, rerender } = render(<HexBattleMap map={map} state={state} visiblePositions={[]} factionNames={new Map()} />)
  const movement = container.querySelector('.unit-direction--movement')!
  const attack = container.querySelector('.unit-direction--attack')!
  const coordinates = (line: Element) => ['x1', 'y1', 'x2', 'y2'].map((name) => Number(line.getAttribute(name)))
  const [mx1, my1, mx2, my2] = movement.getAttribute('points')!.split(/[ ,]/).map(Number)
  expect(mx1).toBeGreaterThan(mx2)
  expect(my1).toBe(my2)
  expect(mx2 - Math.sqrt(3) * 12).toBeCloseTo(17)
  const [x1, y1, , y2] = coordinates(attack)
  expect(Math.hypot(x1 - Math.sqrt(3) * 12, y1 - 36)).toBeCloseTo(17)
  expect(y2).toBeCloseTo(17)
  expect(y1).toBeGreaterThan(y2)
  const tracer = container.querySelector('.attack-tracer')!
  expect(coordinates(tracer)).toEqual(coordinates(attack))
  expect(tracer).toHaveAttribute('aria-hidden', 'true')
  expect(tracer.querySelector('title')).toBeNull()
  expect(tracer).not.toHaveAttribute('marker-end')
  for (const arrow of [movement, attack]) {
    const id = arrow.getAttribute('marker-end')!.slice(5, -1)
    expect(document.getElementById(id)).toHaveAttribute('markerUnits', 'userSpaceOnUse')
  }
  rerender(<HexBattleMap map={map} state={{ ...state, units: state.units.map((unit) => ({ ...unit, movedFrom: undefined, attackTargetPosition: undefined })) }} visiblePositions={[]} factionNames={new Map()} />)
  expect(container.querySelector('.unit-direction, .attack-tracer')).toBeNull()
})

it('omits zero-length and overlapping arrows without reversing short paths', () => {
  const unit = state.units[0]
  for (const target of [unit.position, { x: unit.position.x + 0.1, y: unit.position.y }]) {
    const { container, unmount } = render(<HexBattleMap map={map} state={{ ...state, units: [{ ...unit, movedFrom: target, attackTargetId: undefined, attackTargetPosition: target }] }} visiblePositions={[]} factionNames={new Map()} />)
    expect(container.querySelector('.unit-direction, .attack-tracer')).toBeNull()
    unmount()
  }
})


it('keeps a persistent red damage layer beneath current HP through damage and healing', () => {
  const props = { map, visiblePositions: [], factionNames: new Map<string, string>() }
  const { container, rerender } = render(<HexBattleMap {...props} state={state} />)
  const bar = container.querySelector('.map-entity[aria-label*="Ranger"] .health-value')!
  const damage = bar.previousElementSibling as SVGElement
  expect(damage).toHaveClass('health-damage')
  expect(damage).toHaveAttribute('aria-hidden', 'true')
  expect(parseFloat(damage.style.width)).toBeCloseTo(26 * (20 / 30), 3)
  expect(parseFloat((bar as SVGElement).style.width)).toBeCloseTo(26 * (20 / 30), 3)
  for (const health of [10, 25, 40]) {
    rerender(<HexBattleMap {...props} state={{ ...state, units: [{ ...state.units[0], health }] }} />)
    expect(container.querySelector('.map-entity[aria-label*="Ranger"] .health-value')).toBe(bar)
    expect(parseFloat((bar as SVGElement).style.width)).toBeCloseTo(26 * Math.min(1, health / 30), 3)
    expect(bar.previousElementSibling).toBe(damage)
    expect(parseFloat(damage.style.width)).toBeCloseTo(26 * Math.min(1, health / 30), 3)
  }
})


it('animates recorded bends once, preserves selection, and honors reduced motion', async () => {
  const cancel = vi.fn()
  const animate = vi.fn(() => ({ cancel }))
  const preference = { matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }
  vi.stubGlobal('matchMedia', vi.fn(() => preference))
  const original = Object.getOwnPropertyDescriptor(SVGElement.prototype, 'animate')
  Object.defineProperty(SVGElement.prototype, 'animate', { configurable: true, value: animate })
  try {
    const start = { x: 0, y: 1 }
    const bend = { x: 1, y: 1 }
    const end = { x: 1, y: 0 }
    const props = { map, visiblePositions: [], factionNames: new Map<string, string>() }
    const snapshot = { ...state, units: [{ ...state.units[0], position: start, movedFrom: undefined, attackTargetId: undefined, attackTargetPosition: undefined }] }
    const { container, rerender, unmount } = render(<HexBattleMap {...props} state={snapshot} />)
    expect(animate).not.toHaveBeenCalled()
    const ranger = screen.getByRole('button', { name: /Ranger/ })
    await userEvent.click(ranger)
    const moved = { ...snapshot, tick: 4, units: [{ ...snapshot.units[0], position: end, movedFrom: start, movementPath: [start, bend, end] }] }
    rerender(<HexBattleMap {...props} state={moved} />)
    expect(animate).toHaveBeenCalledTimes(1)
    expect(animate).toHaveBeenLastCalledWith([
      { transform: `translate(${Math.sqrt(3) * 24 * .5}px, 36px)` },
      { transform: `translate(${Math.sqrt(3) * 24 * 1.5}px, 36px)` },
      { transform: `translate(${Math.sqrt(3) * 24}px, 0px)` },
    ], { duration: 440, easing: 'linear' })
    expect(ranger).toHaveAttribute('aria-pressed', 'true')
    expect(container.querySelector('.unit-direction--movement')).toHaveAttribute('points', expect.stringContaining(`${Math.sqrt(3) * 24 * 1.5},36`))
    rerender(<HexBattleMap {...props} state={{ ...moved }} />)
    await userEvent.click(screen.getByRole('button', { name: 'Zoom in' }))
    expect(animate).toHaveBeenCalledTimes(1)
    preference.matches = true
    preference.addEventListener.mock.calls.forEach(([, callback]) => callback())
    expect(cancel).toHaveBeenCalled()
    rerender(<HexBattleMap {...props} state={{ ...snapshot, tick: 5, units: [{ ...snapshot.units[0], movedFrom: end, movementPath: [end, bend, start] }] }} />)
    expect(animate).toHaveBeenCalledTimes(1)
    unmount()
    expect(preference.removeEventListener).toHaveBeenCalled()
  } finally {
    if (original) Object.defineProperty(SVGElement.prototype, 'animate', original)
    else delete (SVGElement.prototype as { animate?: unknown }).animate
    vi.unstubAllGlobals()
  }
})

it('tracks current target coordinates while fixed shots show distinct misses and destroyed-target impacts', () => {
  const attacker = { ...state.units[0], position: { x: 0, y: 0 }, movedFrom: undefined, attackTargetId: 'target', attackTargetPosition: { x: 1, y: 0 } }
  const target = { ...state.units[0], id: 'target', position: { x: 2, y: 0 }, movedFrom: undefined, attackTargetId: undefined, attackTargetPosition: undefined }
  const props = { map, visiblePositions: [], factionNames: new Map<string, string>() }
  const snapshot = { ...state, buildings: [], units: [attacker, target] }
  const { container, rerender } = render(<HexBattleMap {...props} state={snapshot} />)
  expect(Number(container.querySelector('.unit-direction--attack')!.getAttribute('x2'))).toBeCloseTo(Math.sqrt(3) * 48 - 17)
  expect(container.querySelector('.attack-impact')).toBeNull()
  rerender(<HexBattleMap {...props} state={{ ...snapshot, tick: 4, units: [{ ...attacker, attackMode: 'position', attackOutcome: 'miss' }, target] }} />)
  expect(Number(container.querySelector('.unit-direction--attack')!.getAttribute('x2'))).toBeCloseTo(Math.sqrt(3) * 24 - 5)
  const impact = container.querySelector('.attack-impact--miss')!
  expect(impact).toHaveTextContent('missed hex 1, 0')
  expect(container.querySelector('.attack-tracer')).toBeNull()
  rerender(<HexBattleMap {...props} state={{ ...snapshot, tick: 4, units: [{ ...attacker, attackMode: 'position', attackOutcome: 'miss' }, target] }} />)
  expect(container.querySelector('.attack-impact--miss')).toBe(impact)
  rerender(<HexBattleMap {...props} state={{ ...snapshot, tick: 5, units: [{ ...attacker, attackOutcome: 'hit' }] }} />)
  expect(container.querySelector('.attack-impact--hit')).toHaveTextContent('hit')
  expect(container.querySelector('.attack-impact-line')).toBeInTheDocument()
})

it('nudges attackers once per tick, leaves towers still, and cancels for reduced motion', async () => {
  const cancel = vi.fn()
  const animate = vi.fn(() => ({ cancel }))
  const preference = { matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }
  vi.stubGlobal('matchMedia', vi.fn(() => preference))
  const original = Object.getOwnPropertyDescriptor(SVGElement.prototype, 'animate')
  Object.defineProperty(SVGElement.prototype, 'animate', { configurable: true, value: animate })
  try {
    const props = { map, visiblePositions: [], factionNames: new Map<string, string>() }
    const snapshot = { ...state, buildings: [{ ...state.buildings[0], attackTargetId: state.units[0].id, attackTargetPosition: state.units[0].position }] }
    const { rerender } = render(<HexBattleMap {...props} state={snapshot} />)
    expect(animate).toHaveBeenCalledTimes(1)
    const [frames, options] = animate.mock.calls[0] as unknown as [Keyframe[], KeyframeAnimationOptions]
    expect(options.duration).toBe(200)
    const offset = String(frames[1].transform).match(/-?\d+(?:\.\d+)?/g)!.map(Number)
    expect(Math.hypot(...offset)).toBeCloseTo(.75)
    expect(frames[0].transform).toBe(frames[2].transform)
    await userEvent.click(screen.getByRole('button', { name: /Ranger/ }))
    await userEvent.click(screen.getByRole('button', { name: 'Zoom in' }))
    rerender(<HexBattleMap {...props} state={{ ...snapshot }} />)
    expect(animate).toHaveBeenCalledTimes(1)
    rerender(<HexBattleMap {...props} state={{ ...snapshot, tick: 4 }} />)
    expect(animate).toHaveBeenCalledTimes(2)
    preference.matches = true
    preference.addEventListener.mock.calls.at(-1)![1]()
    expect(cancel).toHaveBeenCalled()
    rerender(<HexBattleMap {...props} state={{ ...snapshot, tick: 5 }} />)
    expect(animate).toHaveBeenCalledTimes(2)
  } finally {
    if (original) Object.defineProperty(SVGElement.prototype, 'animate', original)
    else delete (SVGElement.prototype as { animate?: unknown }).animate
    vi.unstubAllGlobals()
  }
})

it('updates tracking arrows from intermediate SVG walking transforms', () => {
  let callback: FrameRequestCallback = () => {}
  vi.stubGlobal('requestAnimationFrame', vi.fn((next: FrameRequestCallback) => { callback = next; return 1 }))
  vi.stubGlobal('cancelAnimationFrame', vi.fn())
  const original = Object.getOwnPropertyDescriptor(SVGElement.prototype, 'getCTM')
  let targetX = 65
  Object.defineProperty(SVGElement.prototype, 'getCTM', { configurable: true, value: function(this: SVGElement) {
    if (this.classList.contains('map-layer')) return { a: 2, d: 2, e: 24, f: 30 }
    const isTarget = this.querySelector('[aria-label*="Striker"]')
    return { a: 2, d: 2, e: 24 + (isTarget ? targetX * 2 : 0), f: 30 }
  } })
  try {
    const attacker = { ...state.units[0], position: { x: 0, y: 0 }, movedFrom: undefined, attackTargetId: 'target', attackTargetPosition: { x: 1, y: 0 } }
    const target = { ...state.units[0], id: 'target', type: 'striker', position: { x: 2, y: 0 }, movedFrom: undefined, attackTargetId: undefined, attackTargetPosition: undefined }
    const { container, unmount } = render(<HexBattleMap map={map} state={{ ...state, buildings: [], units: [attacker, target] }} visiblePositions={[]} factionNames={new Map()} />)
    callback(0)
    const arrow = container.querySelector('.unit-direction--attack')!
    expect(Number(arrow.getAttribute('x2'))).toBeCloseTo(48)
    targetX = 80
    callback(100)
    expect(Number(arrow.getAttribute('x2'))).toBeCloseTo(63)
    expect(container.querySelector('.attack-tracer')).toHaveAttribute('x2', '63')
    unmount()
    expect(cancelAnimationFrame).toHaveBeenCalled()
  } finally {
    if (original) Object.defineProperty(SVGElement.prototype, 'getCTM', original)
    else delete (SVGElement.prototype as { getCTM?: unknown }).getCTM
    vi.unstubAllGlobals()
  }
})


it('shows confirmed deaths once per snapshot and never infers deaths from missing units', () => {
  const props = { map, visiblePositions: [], factionNames: new Map<string, string>() }
  const { container, rerender } = render(<HexBattleMap {...props} state={state} />)
  rerender(<HexBattleMap {...props} state={{ ...state, tick: 4, units: [] }} />)
  expect(container.querySelector('.unit-death')).toBeNull()
  const death = { id: state.units[0].id, factionId: 'a', type: 'ranger', position: { x: 1, y: 0 } }
  const snapshot = { ...state, tick: 5, units: [], unitDeaths: [death] }
  rerender(<HexBattleMap {...props} state={snapshot} />)
  const marker = screen.getByRole('img', { name: /Ranger #1 died/ })
  expect(marker).toHaveAttribute('transform', `translate(${Math.sqrt(3) * 24} 0)`)
  expect(marker).not.toHaveAttribute('tabindex')
  expect(marker).toHaveStyle({ stroke: '#69d2ff' })
  rerender(<HexBattleMap {...props} state={{ ...snapshot }} />)
  expect(screen.getByRole('img', { name: /Ranger #1 died/ })).toBe(marker)
  rerender(<HexBattleMap {...props} state={{ ...state, tick: 6, units: [] }} />)
  expect(container.querySelector('.unit-death')).toBeNull()
})


it('colors movement, attacks, tower fire, and arrowheads by the acting faction', () => {
  const factions = ['a', 'b'].map((factionId) => ({ ...state.factions[0], factionId }))
  const units = ['a', 'b', 'unknown'].map((factionId, index) => ({ ...state.units[0], id: `${factionId}:ranger:1`, factionId, movedFrom: { x: index + 1, y: 1 }, position: { x: index, y: 1 } }))
  const buildings = [{ ...state.buildings[0], attackTargetId: units[1].id, attackTargetPosition: { x: 1, y: 1 } }]
  const { container } = render(<HexBattleMap map={map} state={{ ...state, factions, units, buildings }} visiblePositions={[]} factionNames={new Map()} />)
  const expected = ['#69d2ff', '#ff6b55', '#f2c94c']
  for (const selector of ['.unit-direction--movement', '.unit-direction--attack']) {
    const arrows = [...container.querySelectorAll<SVGElement>(selector)]
    expect(arrows).toHaveLength(selector.endsWith('attack') ? 4 : 3)
    for (const [index, arrow] of arrows.entries()) {
      const color = selector.endsWith('attack') ? ['#69d2ff', ...expected][index] : expected[index]
      expect(arrow).toHaveStyle({ stroke: color })
      const marker = document.getElementById(arrow.getAttribute('marker-end')!.slice(5, -1))!
      expect(marker).toHaveAttribute('fill', color)
    }
  }
})
