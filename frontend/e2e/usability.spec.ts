import { test, expect, battleId, factionId, battleFixture } from './fixtures'

test('navigation, focus, responsive forms and logout', async ({ page, isMobile }) => {
  await page.goto('/')
  await expect(page).toHaveTitle('OrdinalFrontier')
  await expect(page.getByRole('link', { name: 'OrdinalFrontier home' })).toBeVisible()
  await expect(page.locator('.brand__name')).toBeVisible()
  await page.keyboard.press('Tab')
  await expect(page.getByRole('link', { name: 'Skip to main content' })).toBeFocused()
  if (isMobile) {
    await page.getByRole('button', { name: 'Menu', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Log out admin' })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('button', { name: 'Menu', exact: true })).toBeFocused()
    await page.getByRole('button', { name: 'Menu', exact: true }).click()
  }
  await expect(page.getByRole('navigation').getByRole('link')).toHaveText(['Stage battle', 'Factions', 'Battle history', 'Leagues', 'Statistics', 'Admin'])
  await page.getByRole('navigation').getByRole('link', { name: 'Factions', exact: true }).click()
  await page.getByRole('link', { name: '+ CREATE FACTION' }).click()
  await expect(page.getByLabel('Faction name', { exact: true })).toBeInViewport()
  if (isMobile) {
    await expect(page.getByLabel('Faction doctrine preview')).not.toBeVisible()
    await page.getByText('Preview faction', { exact: true }).click()
    await expect(page.getByLabel('Faction doctrine preview')).toBeVisible()
  }
  await expect(page.getByRole('button', { name: 'Generate a new faction identifier' })).toHaveCount(0)
  await page.goto('/battles/new')
  await expect(page.getByLabel('Combat map')).toBeInViewport()
  await expect(page.getByText('AI integration status', { exact: true })).toHaveCount(0)
  await expect(page.getByText('Offline AI', { exact: true })).toHaveCount(0)
  await expect(page.getByLabel('Controller', { exact: true }).first().locator('option')).toHaveCount(1)
  if (isMobile) await page.getByRole('button', { name: 'Menu', exact: true }).click()
  await page.getByRole('button', { name: 'Log out admin' }).click()
  await expect(page.getByRole('heading', { name: 'Identify commander.' })).toBeVisible()
  await expect(page.locator('.login-card__eyebrow')).toHaveText('OrdinalFrontier')
})
test('failed logout stays visible and can be retried', async ({ page, isMobile }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  let attempts = 0
  await page.route('**/v1/auth/logout', route => {
    attempts += 1
    return attempts === 1 ? route.abort('failed') : route.fulfill({ status: 204 })
  })
  await page.goto('/')
  if (isMobile) await page.getByRole('button', { name: 'Menu', exact: true }).click()
  await page.getByRole('button', { name: 'Log out admin' }).click()
  await expect(page.getByRole('alert')).toHaveText('Logout could not be confirmed. Try Log out again.')
  await expect(page.getByRole('alert')).toBeInViewport()
  await expect(page.getByRole('heading', { name: 'Main menu' })).toBeVisible()
  await page.getByRole('button', { name: 'Log out admin' }).click()
  await expect(page.getByRole('heading', { name: 'Identify commander.' })).toBeVisible()
  await expect(page.getByRole('alert')).toHaveCount(0)
  expect(attempts).toBe(2)
  expect(errors).toEqual([])
})

for (const [width, height] of [[15, 11], [25, 17]]) {
  test(`map ${width}x${height}, expansion, focus and result clarity`, async ({ page, api, isMobile }) => {
    api.width = width; api.height = height
    await page.goto(`/battles/${battleId}`)
    await expect(page.getByText('Tick 89 of 500 maximum', { exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: 'PLAY', exact: true })).toHaveCount(0)
    // A settled battle opens on its results, laid over the map without page scroll.
    const results = page.getByRole('region', { name: 'After-action statistics' })
    await expect(results).toBeInViewport()
    if (!isMobile) {
      const cockpit = (await page.locator('.battle-cockpit').boundingBox())!
      expect(cockpit.y + cockpit.height).toBeLessThanOrEqual(page.viewportSize()!.height)
    }
    // Keyboard focus moves from the faction strips straight to the results: narrow screens
    // hide the map, and the covered desktop map's controls cannot take focus.
    const expand = page.getByRole('button', { name: 'Expand map' })
    if (isMobile) await expect(expand).toBeHidden()
    else {
      await expand.focus()
      await expect(expand).not.toBeFocused()
    }
    await page.getByRole('region', { name: 'Faction summaries' }).locator('summary').last().focus()
    await page.keyboard.press('Tab')
    await expect(results.getByRole('button', { name: 'VIEW MAP' })).toBeFocused()
    await results.getByRole('button', { name: 'VIEW MAP' }).click()
    await expect(results).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Show results' })).toBeFocused()
    // Azure's castle is destroyed, so only Amber still has somewhere to build.
    await expect(page.locator('.territory')).toHaveCount(1)
    const inlineMap = page.getByLabel('Map navigation')
    if (isMobile) {
      const dimensions = await inlineMap.evaluate(node => {
        const bounds = node.getBoundingClientRect()
        const [width, height] = getComputedStyle(node).aspectRatio.split('/').map(Number)
        return { actual: bounds.width / bounds.height, expected: width / height }
      })
      expect(dimensions.actual).toBeCloseTo(dimensions.expected, 2)
    } else {
      // Desktop fits the command center to the viewport; the map fills what is left.
      const cockpit = (await page.locator('.battle-cockpit').boundingBox())!
      expect(cockpit.y + cockpit.height).toBeLessThanOrEqual(page.viewportSize()!.height)
      expect((await inlineMap.boundingBox())!.height).toBeGreaterThan(300)
    }
    await page.getByRole('button', { name: 'Amber · Castle', exact: true }).click()
    await page.getByRole('button', { name: 'Expand map' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: 'Pan map with arrow keys' }).focus()
    await page.keyboard.press('Shift+Tab')
    await expect.poll(() => dialog.evaluate(node => node.contains(document.activeElement))).toBe(true)
    await page.keyboard.press('Tab')
    await expect(dialog.getByRole('button', { name: 'Pan map with arrow keys' })).toBeFocused()
    await expect(dialog.getByRole('button', { name: 'Amber · Castle', exact: true })).toHaveAttribute('aria-pressed', 'true')
    await dialog.getByRole('button', { name: 'Zoom in' }).click()
    await dialog.getByRole('button', { name: 'Pan map with arrow keys' }).focus()
    const layer = dialog.locator('.map-layer')
    const before = await layer.getAttribute('transform')
    await page.keyboard.press('ArrowRight')
    await expect(layer).not.toHaveAttribute('transform', before!)
    const viewport = dialog.getByLabel('Map navigation')
    const bounds = (await viewport.boundingBox())!
    const panned = await layer.getAttribute('transform')
    await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2)
    await page.mouse.down()
    await page.mouse.move(bounds.x + bounds.width / 2 + 40, bounds.y + bounds.height / 2 + 20)
    await page.mouse.up()
    await expect(layer).not.toHaveAttribute('transform', panned!)
    await dialog.getByRole('button', { name: 'Fit map' }).click()
    await expect(layer).toHaveAttribute('transform', 'translate(24 30) scale(1)')
    await page.keyboard.press('Escape')
    await expect(dialog).not.toBeVisible()
    await expect(page.getByRole('button', { name: 'Expand map' })).toBeFocused()
    await expect(page.getByLabel('Perspective')).toHaveCount(0)
    if (!isMobile) {
      const mapBounds = (await page.locator('.battle-map-column .map-panel').boundingBox())!
      const stripBounds = (await page.getByRole('region', { name: 'Faction summaries' }).boundingBox())!
      const inspectorBounds = (await page.locator('.battle-inspector').boundingBox())!
      expect(inspectorBounds.y).toBeCloseTo(stripBounds.y, 0)
      expect(inspectorBounds.y + inspectorBounds.height).toBeCloseTo(mapBounds.y + mapBounds.height, 0)
    }
    expect(api.writes).toEqual([])
  })
}
test('a settled battle replays inside the command center', async ({ page, isMobile }) => {
  await page.goto(`/battles/${battleId}`)
  await page.getByRole('button', { name: 'WATCH REPLAY' }).click()
  const controls = page.getByRole('region', { name: 'Replay controls' })
  await expect(page.getByText('HEX TACTICAL MAP · TICK 0')).toBeVisible()
  await controls.getByRole('slider', { name: 'Replay tick' }).fill('85')
  await expect(page.getByText('HEX TACTICAL MAP · TICK 85')).toBeVisible()
  await expect(page.getByRole('region', { name: /Tick \d+ events/ })).toHaveCount(0)
  await controls.getByRole('combobox', { name: 'Speed' }).selectOption('8')
  await controls.getByRole('button', { name: 'PLAY', exact: true }).click()
  await expect(page.getByText('HEX TACTICAL MAP · TICK 89')).toBeVisible()
  await expect(controls.getByRole('button', { name: 'PLAY', exact: true })).toBeVisible()
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  if (!isMobile) {
    const cockpit = (await page.locator('.battle-cockpit').boundingBox())!
    expect(cockpit.y + cockpit.height).toBeLessThanOrEqual(page.viewportSize()!.height)
    expect((await page.getByLabel('Map navigation').boundingBox())!.height).toBeGreaterThan(250)
  }
  await controls.getByRole('button', { name: 'EXIT REPLAY' }).click()
  // Leaving the replay returns to the results screen over the final map.
  await page.getByRole('region', { name: 'After-action statistics' }).getByRole('button', { name: 'VIEW MAP' }).click()
  await expect(page.getByText('HEX TACTICAL MAP · TICK 89')).toBeVisible()
  await expect(page.getByRole('button', { name: 'WATCH REPLAY' })).toBeVisible()
})

test('a battle that settles while the map is expanded keeps the dialog usable', async ({ page }) => {
  const battle = { ...battleFixture(), status: 'running', currentTick: 80, result: null as ReturnType<typeof battleFixture>['result'] | null }
  await page.route(`**/v1/battles/${battleId}`, route => route.fulfill({ json: battle }))
  await page.goto(`/battles/${battleId}`)
  await page.getByRole('button', { name: 'Expand map' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  Object.assign(battle, { status: 'completed', currentTick: 89, result: battleFixture().result })
  await expect(page.getByRole('region', { name: 'After-action statistics' })).toBeAttached()
  await dialog.getByRole('button', { name: 'Zoom in' }).click()
  await dialog.getByRole('button', { name: 'Close map' }).click()
  await expect(dialog).not.toBeVisible()
  await expect(page.getByRole('region', { name: 'After-action statistics' })).toBeVisible()
})

test('the full map stays available without faction-view requests', async ({ page }) => {
  const liveBattle = battleFixture()
  liveBattle.state.buildings[1].health = 100
  await page.route(`**/v1/battles/${battleId}`, route => route.fulfill({ json: liveBattle }))
  let viewRequests = 0
  await page.route('**/view', route => { viewRequests += 1; return route.abort('failed') })
  await page.goto(`/battles/${battleId}`)
  await page.getByRole('button', { name: 'VIEW MAP' }).click()
  await expect(page.getByRole('button', { name: 'Amber · Castle', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Azure · Castle', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Expand map' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByLabel('Map navigation')).toBeVisible()
  await expect(dialog.getByLabel('Perspective')).toHaveCount(0)
  await expect(dialog.getByRole('button', { name: 'Azure · Castle', exact: true })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('button', { name: 'Expand map' })).toBeFocused()
  expect(viewRequests).toBe(0)
})

test('key events, all events and details preserve loaded history', async ({ page, isMobile }) => {
  await page.goto(`/battles/${battleId}`)
  await expect(page.getByLabel('Event group')).toHaveValue('key')
  const rows = page.locator('.event-list > li')
  await expect(rows).toHaveCount(isMobile ? 10 : 12)
  if (isMobile) { await page.getByRole('button', { name: 'Show more events' }).click(); await expect(rows).toHaveCount(12) }
  await page.getByLabel('Event group').selectOption('all')
  if (isMobile) await page.getByRole('button', { name: 'Show more events' }).click()
  await expect(rows).toHaveCount(14)
  await page.getByText('Details · Inspect event #1', { exact: true }).click()
  await expect(rows.last().locator('pre')).toContainText(factionId)
  await page.getByLabel('Event group').selectOption('combat')
  await expect(rows).toHaveCount(1)
})
test('availability and hidden identifiers keep manual editing simple', async ({ page, api }) => {
  await page.goto('/factions/new')
  await expect(page.getByText('Disabled by operator')).toBeVisible()
  await expect(page.getByText(/generations left today/)).toHaveCount(0)
  await page.getByLabel('Background (optional)').fill('Manually written.')
  await expect(page.getByRole('button', { name: /Copy.*identifier/i })).toHaveCount(0)
  await expect(page.locator('.identifier')).toHaveCount(0)
  api.generation = { ...api.generation, available: true, remaining: 0 }
  await page.getByRole('button', { name: 'Check availability', exact: true }).click()
  await expect(page.getByText("Today's generation allowance is exhausted.")).toBeVisible()
  api.generation = { ...api.generation, remaining: 10, retryAt: new Date(Date.now() + 60_000).toISOString() }
  await page.getByRole('button', { name: 'Check availability', exact: true }).click()
  await expect(page.getByText(/Available again in/)).toBeVisible()
  await expect(page.getByLabel('Background (optional)')).toHaveValue('Manually written.')
})
test('deletion cancellation, failure, retry and redirect', async ({ page, api }) => {
  await page.goto(`/factions/${factionId}/edit`)
  const remove = page.getByRole('button', { name: 'DELETE Amber' })
  page.once('dialog', dialog => dialog.dismiss())
  await remove.click()
  expect(api.writes).toEqual([])
  api.deleteFails = true
  page.once('dialog', dialog => dialog.accept())
  await remove.click()
  await expect(page.getByRole('alert')).toContainText('could not be deleted')
  await expect(remove).toBeEnabled()
  api.deleteFails = false
  page.once('dialog', dialog => dialog.accept())
  await remove.click()
  await expect(page).toHaveURL('/factions')
  await expect(page.getByRole('heading', { name: 'Amber', exact: true })).toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'Azure', exact: true })).toBeVisible()
})
test('failed availability reads can be retried without inventing configuration advice', async ({ page, api }) => {
  const endpoint = '**/v1/faction-background-generations/options'
  await page.route(endpoint, route => route.fulfill({ status: 503 }))
  await page.goto('/factions/new')
  await expect(page.getByRole('alert')).toContainText('availability could not be established')
  await page.getByLabel('Background (optional)').fill('Manual background remains editable.')
  await page.unroute(endpoint)
  api.generation = { ...api.generation, reason: '' }
  await page.getByRole('button', { name: 'Check availability', exact: true }).click()
  await expect(page.getByText('Background writer availability could not be established. You can still write a background.')).toBeVisible()
  await expect(page.getByText(/generations left today/)).toHaveCount(0)
  await expect(page.getByLabel('Background (optional)')).toHaveValue('Manual background remains editable.')
})
// One test per width keeps each within the default timeout on a shared CI host.
for (const width of [390, 768, 1440]) {
  test(`pages reflow at ${width}px width`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    for (const path of ['/', '/factions', '/factions/new', '/battles/new', '/battles', `/battles/${battleId}`]) {
      await page.goto(path)
      await expect(page.getByRole('main')).toBeVisible()
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    }
  })
}

test('terrain icons, specialized production and destroyed buildings on the map', async ({ page }) => {
  const battle = battleFixture()
  battle.map.rows[3] = '..fg#..........'
  const types = ['barracks', 'archery_range', 'stables', 'siege_workshop', 'infirmary']
  const units = ['vanguard', 'ranger', 'scout', 'catapult', 'herbalist']
  battle.state.buildings.push(...types.map((type, index) => ({ ...battle.state.buildings[0], id: `${factionId}:${type}:1`, type, position: { x: 2 + index * 2, y: 5 }, income: 0, trains: [units[index]] })))
  Object.assign(battle.state.buildings[0], { trains: ['vanguard', 'striker', 'pikeman'], attack: 15, range: 3 })
  await page.route(`**/v1/battles/${battleId}`, route => route.fulfill({ json: battle }))
  await page.goto(`/battles/${battleId}`)
  await page.getByRole('button', { name: 'VIEW MAP' }).click()
  const map = page.getByRole('region', { name: 'Castle siege map', exact: true })
  for (const terrain of ['forest', 'gold', 'rock']) await expect(map.locator(`.terrain-glyph--${terrain}`)).toHaveCount(1)
  await expect(map.getByRole('button', { name: 'Azure · Castle' })).toHaveCount(0)
  for (const label of ['Barracks', 'Archery Range', 'Stables', 'Siege Workshop', 'Infirmary']) {
    await map.getByRole('button', { name: `Amber · ${label} #1` }).click()
    await expect(map.getByText('Training queue empty')).toBeVisible()
  }
  await map.getByRole('button', { name: 'Amber · Castle', exact: true }).click()
  await expect(map.getByText('Training queue empty')).toBeVisible()
  await expect(map.getByText(/Trains: Vanguard, Striker, Pikeman/)).toBeVisible()
  await expect(map.getByText(/Attack 15/)).toBeVisible()
  await expect(map.getByText(/Range 3/)).toBeVisible()
})

test('players can see continuous production progress and pauses without controls', async ({ page }) => {
  const battle = battleFixture()
  Object.assign(battle.state.buildings[0], { trains: ['vanguard'], production: { unitType: 'striker', enabled: false }, trainingQueue: [{ unitType: 'vanguard', remainingTicks: 2, trainingTicks: 4, queuedAtTick: 0 }] })
  battle.state.buildings.push({ ...battle.state.buildings[0], id: `${factionId}:barracks:1`, type: 'barracks', position: { x: 4, y: 5 } })
  Object.assign(battle.state.buildings.at(-1)!, { production: { unitType: 'vanguard', enabled: true, waitingForGold: true }, trainingQueue: [] })
  await page.route('**/v1/auth/session', route => route.fulfill({ json: { enabled: true, authenticated: true, actor: { id: 'viewer', role: 'player' } } }))
  await page.route(`**/v1/battles/${battleId}`, route => route.fulfill({ json: battle }))
  await page.goto(`/battles/${battleId}`)
  const panel = page.getByRole('list', { name: 'Amber production', exact: true })
  await expect(panel).toBeVisible()
  await expect(panel.getByRole('listitem').first()).toHaveAttribute('title', /Finishing paid unit, then stopping/)
  await expect(panel.getByRole('listitem').last()).toHaveAttribute('title', /Waiting for gold/)
  await expect(panel.getByRole('progressbar')).toHaveAttribute('value', '2')
  await expect(panel.locator('button')).toHaveCount(0)
  // Details open by touch or keyboard, not only by hover.
  await panel.locator('summary').last().click()
  await expect(panel.getByText(/Waiting for gold/).last()).toBeVisible()
  await panel.locator('summary').first().focus()
  await page.keyboard.press('Enter')
  await expect(panel.locator('.production-chip__detail').first()).toBeVisible()
  await expect(panel.locator('.production-chip__detail').first()).toHaveText(/Finishing paid unit, then stopping/)
  await expect(panel.locator('.production-chip__detail').last()).toBeHidden()
  await panel.scrollIntoViewIfNeeded()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy()
})

test('players can read researched upgrades from their icons by hover, touch or keyboard', async ({ page, isMobile }) => {
  const battle = battleFixture()
  Object.assign(battle.state.factions[0], { ownedUpgradeIds: ['arsenal', 'industry', 'arsenal_2', 'plating', 'industry_2', 'industry_3', 'gunpowder', 'knighthood', 'vanguard_bulwark', 'ranger_skirmish', 'herbalist_potency', 'siege_breach'] })
  Object.assign(battle.state.buildings[0], { trains: ['vanguard'], production: { unitType: 'vanguard', enabled: true } })
  await page.route('**/v1/auth/session', route => route.fulfill({ json: { enabled: true, authenticated: true, actor: { id: 'viewer', role: 'player' } } }))
  await page.route(`**/v1/battles/${battleId}`, route => route.fulfill({ json: battle }))
  await page.goto(`/battles/${battleId}`)
  const upgrades = page.getByRole('group', { name: 'Amber upgrades', exact: true })
  await expect(upgrades).toBeVisible()
  const items = upgrades.locator('.upgrade-chips__item')
  await expect(items.nth(0).locator('.upgrade-chips__level')).toHaveText('II')
  await expect(items.nth(1).locator('.upgrade-chips__level')).toHaveText('III')
  await expect(page.getByRole('group', { name: 'Azure upgrades', exact: true })).toHaveCount(0)
  if (!isMobile) {
    // Hovering an icon names the upgrade and its effect, without widening the page.
    const tooltip = (index: number) => items.nth(index).locator('.upgrade-chips__tooltip')
    await expect(tooltip(0)).toBeHidden()
    await items.nth(0).hover()
    await expect(tooltip(0)).toHaveText('Arsenal II+4 attack for every attacking unit')
    await items.last().hover()
    await expect(tooltip(0)).toBeHidden()
    await expect(tooltip(-1)).toHaveText('Siege: Breach (+8 building damage)Catapults, Battering Rams and Cannons deal 8 more damage to buildings')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy()
  }
  const detail = upgrades.locator('.upgrade-chips__detail')
  await expect(detail).toBeHidden()
  await upgrades.locator('summary').click()
  await expect(detail).toContainText('Knighthood (unlocks Heavy Cavalry)Stables can train Heavy Cavalry')
  // Opening a production chip in the same strip closes the upgrade detail.
  await page.getByRole('list', { name: 'Amber production', exact: true }).locator('summary').click()
  await expect(detail).toBeHidden()
  await upgrades.locator('summary').focus()
  await page.keyboard.press('Enter')
  await expect(detail).toContainText('Herbalist: Potency (+2 treatment)')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy()
})

test('shows the complete failure reason for a failed battle', async ({ page }) => {
  const reason = `Engine halted: ${'worker lease lost while persisting the snapshot; '.repeat(8)}end of reason.`
  await page.route(`**/v1/battles/${battleId}`, route => route.fulfill({ json: { ...battleFixture(), status: 'failed', result: null, errorMessage: reason } }))
  await page.goto(`/battles/${battleId}`)
  const message = page.getByRole('complementary', { name: 'Battle result' }).getByText(reason)
  await expect(message).toBeVisible()
  expect(await message.evaluate(node => node.scrollHeight <= node.clientHeight)).toBe(true)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
})
