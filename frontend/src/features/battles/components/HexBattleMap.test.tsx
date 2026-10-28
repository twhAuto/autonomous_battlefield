// Checks hex-map terrain, accessible entity details, selection, and map controls.

import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { HexBattleMap } from './HexBattleMap'
import type { BattleBuilding, BattleMap, BattleState } from '../api/battles'

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
  it('opens the map dialog, observes viewport changes, and restores page state on close', async () => {
    const disconnect = vi.fn()
    const observe = vi.fn()
    class ResizeObserverMock {
      observe = observe
      disconnect = disconnect
    }
    const showModal = vi.fn(function (this: HTMLDialogElement) { this.setAttribute('open', '') })
    const close = vi.fn(function (this: HTMLDialogElement) { this.removeAttribute('open') })
    const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal')
    const originalClose = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'close')
    vi.stubGlobal('ResizeObserver', ResizeObserverMock)
    Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value: showModal })
    Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value: close })
    try {
      document.body.style.overflow = 'scroll'
      const { unmount } = render(<HexBattleMap map={map} state={state} factionNames={new Map()} />)

      const expand = screen.getByRole('button', { name: 'Expand map' })
      await userEvent.click(expand)
      expect(showModal).toHaveBeenCalledOnce()
      expect(observe).toHaveBeenCalled()
      expect(document.body.style.overflow).toBe('hidden')
      await userEvent.click(screen.getByRole('button', { name: 'Close map' }))
      expect(close).toHaveBeenCalled()
      expect(document.body.style.overflow).toBe('scroll')
      expect(screen.getByRole('button', { name: 'Expand map' })).toHaveFocus()

      unmount()
      expect(disconnect).toHaveBeenCalled()
    } finally {
      vi.unstubAllGlobals()
      if (originalShowModal) Object.defineProperty(HTMLDialogElement.prototype, 'showModal', originalShowModal)
      else delete (HTMLDialogElement.prototype as { showModal?: unknown }).showModal
      if (originalClose) Object.defineProperty(HTMLDialogElement.prototype, 'close', originalClose)
      else delete (HTMLDialogElement.prototype as { close?: unknown }).close
      document.body.style.overflow = ''
    }
  })

  it('preserves camera and selected entity across live tick updates', async () => {
    const props = { map, state, factionNames: new Map([['a', 'Northwind']]) }
    const { container, rerender } = render(<HexBattleMap {...props} />)
    await userEvent.click(screen.getByRole('button', { name: 'Zoom in' }))
    fireEvent.keyDown(screen.getByRole('button', { name: 'Pan map with arrow keys' }), { key: 'ArrowRight' })
    fireEvent.keyDown(screen.getByRole('button', { name: 'Northwind · Castle' }), { key: ' ' })
    const camera = container.querySelector('.map-layer')!.getAttribute('transform')
    rerender(<HexBattleMap {...props} state={{ ...state, tick: 4 }} />)
    expect(container.querySelector('.map-layer')).toHaveAttribute('transform', camera)
    expect(screen.getByRole('button', { name: 'Northwind · Castle' })).toHaveAttribute('aria-pressed', 'true')
  })
  it('renders terrain, accessible entities, selection, and map controls', async () => {
    const user = userEvent.setup()
    const { container } = render(<HexBattleMap map={map} state={state} factionNames={new Map([['a', 'Northwind']])} />)
    const battlefield = container.querySelector('svg')!
    expect(battlefield).toHaveAccessibleName(/2 by 2 pointy-top hex battlefield/i)
    expect(container.querySelectorAll('.hex-tile')).toHaveLength(4)
    expect(container.querySelectorAll('.hex-tile--fog')).toHaveLength(0)
    expect(container.querySelectorAll('.unit-direction--movement')).toHaveLength(1)
    expect(container.querySelectorAll('.unit-direction--attack')).toHaveLength(1)
    expect(screen.getByText(/Northwind · Ranger #1 attacked Northwind · Castle/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Northwind · Striker · a:strike' })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Visible entity summary' })).not.toBeInTheDocument()
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
  const { container, rerender } = render(<HexBattleMap map={map} state={snapshot} factionNames={new Map([['a', 'Northwind']])} />)
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
  await userEvent.click(screen.getByText('Map legend'))
  expect(screen.getByText('Battering Ram', { exact: true })).toBeVisible()
  expect(screen.getByText('Defense Tower', { exact: true })).toBeVisible()
  expect(screen.getByText('New Scout', { exact: true })).toBeVisible()
  // Polling reorders living units and changes HP without changing selection/names.
  rerender(<HexBattleMap map={map} state={{ ...snapshot, units: [...snapshot.units].reverse().map((unit) => ({ ...unit, health: 10 })) }} factionNames={new Map([['a', 'Northwind']])} />)
  expect(screen.getByRole('button', { name: 'Northwind · New Scout #8' })).toHaveAttribute('aria-pressed', 'true')
  expect(container.querySelector('.entity-details')).toHaveTextContent('HP 10/30')
})


it('clears adjacent icons and removes decorative tracers when actions end', () => {
  const { container, rerender } = render(<HexBattleMap map={map} state={state} factionNames={new Map()} />)
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
  rerender(<HexBattleMap map={map} state={{ ...state, units: state.units.map((unit) => ({ ...unit, movedFrom: undefined, attackTargetPosition: undefined })) }} factionNames={new Map()} />)
  expect(container.querySelector('.unit-direction, .attack-tracer')).toBeNull()
})

it('omits zero-length and overlapping arrows without reversing short paths', () => {
  const unit = state.units[0]
  for (const target of [unit.position, { x: unit.position.x + 0.1, y: unit.position.y }]) {
    const { container, unmount } = render(<HexBattleMap map={map} state={{ ...state, units: [{ ...unit, movedFrom: target, attackTargetId: undefined, attackTargetPosition: target }] }} factionNames={new Map()} />)
    expect(container.querySelector('.unit-direction, .attack-tracer')).toBeNull()
    unmount()
  }
})


it('keeps a persistent red damage layer beneath current HP through damage and healing', () => {
  const props = { map, factionNames: new Map<string, string>() }
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
    const props = { map, factionNames: new Map<string, string>() }
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
  const props = { map, factionNames: new Map<string, string>() }
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
    const props = { map, factionNames: new Map<string, string>() }
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
    const { container, unmount } = render(<HexBattleMap map={map} state={{ ...state, buildings: [], units: [attacker, target] }} factionNames={new Map()} />)
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
  const props = { map, factionNames: new Map<string, string>() }
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
  const { container } = render(<HexBattleMap map={map} state={{ ...state, factions, units, buildings }} factionNames={new Map()} />)
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


it('keeps each faction\'s color when state lists only one faction', () => {
  const units = ['b', 'a'].map((factionId, index) => ({ ...state.units[0], id: `${factionId}:ranger:1`, factionId, movedFrom: { x: index + 1, y: 1 }, position: { x: index, y: 1 } }))
  const names = new Map([['b', 'Southreach'], ['a', 'Northwind']])
  const colorsFor = (factions: typeof state.factions) => {
    const { container, unmount } = render(<HexBattleMap map={map} state={{ ...state, factions, units, buildings: [] }} factionNames={names} />)
    const strokes = [...container.querySelectorAll<SVGElement>('.unit-direction--movement')].map((arrow) => arrow.style.stroke)
    unmount()
    return strokes
  }
  const spectator = colorsFor(['a', 'b'].map((factionId) => ({ ...state.factions[0], factionId })))
  expect(spectator).toHaveLength(2)
  for (const own of ['a', 'b']) {
    expect(colorsFor([{ ...state.factions[0], factionId: own }])).toEqual(spectator)
  }
})

it('shows special terrain, building reach and a sequential training queue without fog', async () => {
  const user = userEvent.setup()
  const specialMap = { ...map, rows: ['fg', '..'] }
  const trainingState: BattleState = structuredClone(state)
  trainingState.buildings![0] = { ...trainingState.buildings![0], type: 'archery_range', id: 'a:archery_range:1', trains: ['ranger', 'crossbowman'], buildRange: 4, trainingQueue: [
    { unitType: 'ranger', remainingTicks: 1, trainingTicks: 3, queuedAtTick: 0 },
    { unitType: 'crossbowman', remainingTicks: 8, trainingTicks: 8, queuedAtTick: 0 },
  ] }
  const props = { map: specialMap, state: trainingState, factionNames: new Map([['a', 'Northwind']]) }
  const { container, rerender } = render(<HexBattleMap {...props} />)
  expect(container.querySelectorAll('.hex-tile--fog')).toHaveLength(0)
  expect(container.querySelectorAll('.hex-tile--forest')).toHaveLength(1)
  expect(container.querySelectorAll('.hex-tile--gold')).toHaveLength(1)
  expect(container.querySelectorAll('.terrain-glyph--forest')).toHaveLength(1)
  expect(container.querySelectorAll('.terrain-glyph--gold')).toHaveLength(1)
  expect(screen.getByText(/Forest: \+3 troop defense/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Northwind · Archery Range #1' }))
  expect(container.querySelectorAll('.hex-tile--build-range')).toHaveLength(4)
  expect(screen.getByText(/Build range 4/)).toBeInTheDocument()
  expect(screen.getByText('Ranger · 1/3 ticks remaining')).toBeInTheDocument()
  expect(screen.getByText('Crossbowman · 8/8 ticks remaining')).toBeInTheDocument()
  expect(screen.getByRole('progressbar', { name: 'Training progress' })).toHaveAttribute('value', '2')
  trainingState.buildings![0].trainingQueue![0].remainingTicks = 0
  trainingState.buildings![0].trainingBlocked = true
  rerender(<HexBattleMap {...props} />)
  expect(screen.getByText('Ranger · Ready · exit blocked')).toBeInTheDocument()
  trainingState.buildings![0].trainingQueue = []
  rerender(<HexBattleMap {...props} />)
  expect(screen.getByText('Training queue empty')).toBeInTheDocument()
})

it('removes destroyed buildings, queues and selected range from the battlefield', async () => {
  const current: BattleState = structuredClone(state)
  current.buildings![0] = { ...current.buildings![0], type: 'barracks', trains: ['vanguard'], trainingQueue: [{ unitType: 'vanguard', trainingTicks: 4, remainingTicks: 2, queuedAtTick: 0 }] }
  const props = { map, state: current, factionNames: new Map() }
  const { container, rerender } = render(<HexBattleMap {...props} />)
  await userEvent.click(screen.getByRole('button', { name: /Barracks/ }))
  expect(screen.getByText('Training queue · 1/12')).toBeInTheDocument()
  current.buildings![0].health = 0
  rerender(<HexBattleMap {...props} />)
  expect(screen.queryByRole('button', { name: /Barracks/ })).not.toBeInTheDocument()
  expect(screen.queryByText(/Training queue/)).not.toBeInTheDocument()
  expect(container.querySelectorAll('.hex-tile--build-range')).toHaveLength(0)
  expect(screen.getByText('Select a castle, building, or unit for details.')).toBeInTheDocument()
})

it('preserves the display of historical castles without a training roster', async () => {
  render(<HexBattleMap map={map} state={state} factionNames={new Map()} />)
  await userEvent.click(screen.getByRole('button', { name: /Castle/ }))
  expect(screen.queryByText(/Training queue/)).not.toBeInTheDocument()
})


it('shows castle training, attack range and tower fire together', async () => {
  const castleState: BattleState = { ...state, buildings: [{ ...state.buildings[0], trains: ['vanguard', 'striker', 'pikeman'], attack: 15, range: 3, attackTargetId: state.units[0].id, attackTargetPosition: state.units[0].position }] }
  render(<HexBattleMap map={map} state={castleState} factionNames={new Map()} />)
  await userEvent.click(screen.getByRole('button', { name: /Castle/ }))
  expect(screen.getByText('Training queue empty')).toBeInTheDocument()
  expect(screen.getByText(/Trains: Vanguard, Striker, Pikeman/)).toBeInTheDocument()
  expect(screen.getByText(/Attack 15/)).toBeInTheDocument()
  expect(screen.getByText(/Range 3/)).toBeInTheDocument()
  expect(document.querySelector('.unit-direction--tower')).toBeInTheDocument()
})

it('tints each faction\'s legal build sites and names who can build on a tile', () => {
  const area: BattleMap = { kind: 'authored', width: 13, height: 3, rows: ['...g.........', '.............', 'f..#.........'], castleSpawns: [{ x: 1, y: 1 }, { x: 6, y: 1 }], economySites: [[], []] }
  const building = (id: string, factionId: string, x: number, y: number, extra: Partial<BattleBuilding> = {}): BattleBuilding => ({ id, factionId, type: 'castle', position: { x, y }, health: 100, maxHealth: 100, defense: 1, vision: 1, income: 0, buildRange: 3, ...extra })
  const buildings = [
    building('a:castle', 'a', 1, 1, { buildRange: 2 }), building('b:castle', 'b', 6, 1),
    // Not active until tick 9: it blocks its neighbours but grants no range yet.
    building('a:outpost:1', 'a', 10, 1, { type: 'outpost', buildRange: 5, readyAtTick: 9 }),
    building('a:economy:2', 'a', 4, 2, { type: 'economy', health: 0 }),
  ]
  const { container } = render(<HexBattleMap map={area} state={{ battleId: 'x', tick: 4, factions: [], units: [], buildings }} factionNames={new Map([['a', 'Amber'], ['b', 'Blue']])} />)
  const tile = (x: number, y: number) => container.querySelectorAll('.hex-tile')[y * area.width + x].querySelector('title')!.textContent
  expect(container.querySelectorAll('.build-area')).toHaveLength(2)
  expect(tile(0, 0)).toBe('Ground: build within friendly range, leaving one hex between buildings · (0, 0) · Amber can build here')
  expect(tile(3, 0)).toMatch(/^Gold deposit.*· Amber can build here$/)
  expect(tile(3, 1)).toMatch(/· Amber and Blue can build here$/) // a destroyed neighbour blocks nothing
  expect(tile(9, 0)).toMatch(/\) · Blue can build here$/)
  for (const [x, y] of [[0, 2], [3, 2], [2, 1], [9, 1], [12, 1]]) {
    // Forest, rock, beside a castle, beside the inactive outpost, and its unreached range.
    expect(tile(x, y)).not.toMatch(/can build/)
  }
})

it('adds page actions to the toolbar outside the expanded dialog', async () => {
  const dialog = HTMLDialogElement.prototype
  const original = { showModal: Object.getOwnPropertyDescriptor(dialog, 'showModal'), close: Object.getOwnPropertyDescriptor(dialog, 'close') }
  Object.defineProperty(dialog, 'showModal', { configurable: true, value: function (this: HTMLDialogElement) { this.setAttribute('open', '') } })
  Object.defineProperty(dialog, 'close', { configurable: true, value: function (this: HTMLDialogElement) { this.removeAttribute('open') } })
  const view = render(<HexBattleMap map={map} state={state} factionNames={new Map()} actions={<button type="button">Show results</button>} />)
  try {
    expect(screen.getByRole('button', { name: 'Show results' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Expand map' }))
    expect(screen.queryByRole('button', { name: 'Show results' })).not.toBeInTheDocument()
  } finally {
    view.unmount()
    for (const [name, descriptor] of Object.entries(original)) {
      if (descriptor) Object.defineProperty(dialog, name, descriptor)
      else delete (dialog as unknown as Record<string, unknown>)[name]
    }
  }
})

it('clears the selection from empty ground or the details, but not when a pan ends', async () => {
  const user = userEvent.setup()
  const { container } = render(<HexBattleMap map={map} state={state} factionNames={new Map([['a', 'Amber']])} />)
  const details = () => container.querySelector('.entity-details')!
  const castle = screen.getByRole('button', { name: 'Amber · Castle' })
  const svg = container.querySelector('.map-viewport svg')!
  const tile = container.querySelectorAll('.hex-tile')[1]

  await user.click(castle)
  expect(details()).toHaveTextContent('Amber · Castle')
  // A drag that pans the map ends in a click on the ground; the selection survives it.
  fireEvent.pointerDown(svg, { clientX: 10, clientY: 10 })
  fireEvent.pointerMove(svg, { clientX: 60, clientY: 30 })
  fireEvent.pointerUp(svg)
  fireEvent.click(tile)
  expect(details()).toHaveTextContent('Amber · Castle')
  // Clicking an entity keeps (or moves) the selection rather than clearing it.
  fireEvent.pointerDown(svg, { clientX: 10, clientY: 10 })
  fireEvent.click(castle)
  expect(castle).toHaveAttribute('aria-pressed', 'true')
  fireEvent.pointerDown(svg, { clientX: 10, clientY: 10 })
  fireEvent.click(tile)
  expect(details()).toHaveTextContent('Select a castle, building, or unit for details.')
  expect(castle).toHaveAttribute('aria-pressed', 'false')

  await user.click(castle)
  await user.click(screen.getByRole('button', { name: 'Clear selection' }))
  expect(details()).toHaveTextContent('Select a castle, building, or unit for details.')
  expect(castle).toHaveFocus()
})
