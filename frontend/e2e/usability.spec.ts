import { test, expect, battleId, factionId } from './fixtures'

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
  await expect(page.getByRole('navigation').getByRole('link')).toHaveText(['Stage battle', 'Factions', 'Battle history', 'Admin'])
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
  await expect(page.getByLabel('Combat scenario')).toBeInViewport()
  await page.getByText('AI integration status', { exact: true }).click()
  await expect(page.getByText('Offline AI', { exact: true })).toBeVisible()
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
    const inlineMap = page.getByLabel('Map navigation')
    const dimensions = await inlineMap.evaluate(node => {
      const bounds = node.getBoundingClientRect()
      const [width, height] = getComputedStyle(node).aspectRatio.split('/').map(Number)
      return { actual: bounds.width / bounds.height, expected: width / height }
    })
    expect(dimensions.actual).toBeCloseTo(dimensions.expected, 2)
    await page.getByRole('button', { name: 'Amber · Castle', exact: true }).click()
    await page.getByRole('button', { name: 'Expand map' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await dialog.getByLabel('Perspective').focus()
    await page.keyboard.press('Shift+Tab')
    await expect.poll(() => dialog.evaluate(node => node.contains(document.activeElement))).toBe(true)
    await page.keyboard.press('Tab')
    await expect(dialog.getByLabel('Perspective')).toBeFocused()
    await expect(dialog.getByRole('button', { name: 'Amber · Castle', exact: true })).toHaveAttribute('aria-pressed', 'true')
    await dialog.getByLabel('Perspective').selectOption(factionId)
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
    await expect(page.getByLabel('Perspective')).toHaveValue(factionId)
    if (!isMobile) {
      const mapBounds = (await page.locator('.battle-map-column .map-panel').boundingBox())!
      const inspectorBounds = (await page.locator('.battle-inspector').boundingBox())!
      expect(inspectorBounds.y).toBeCloseTo(mapBounds.y, 0)
      expect(inspectorBounds.y + inspectorBounds.height).toBeCloseTo(mapBounds.y + mapBounds.height, 0)
    }
    expect(api.writes).toEqual([])
  })
}
test('failed faction views retain the expanded map controls and can be retried', async ({ page }) => {
  await page.route('**/view', route => route.fulfill({ status: 503, body: 'Unavailable' }))
  await page.goto(`/battles/${battleId}`)
  await page.getByRole('button', { name: 'Expand map' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Perspective').selectOption(factionId)
  await expect(dialog.getByRole('alert')).toContainText('Faction view could not be loaded.')
  await expect(dialog.getByLabel('Map navigation')).toHaveCount(0)
  await expect(dialog.getByLabel('Perspective')).toHaveValue(factionId)
  await page.unroute('**/view')
  await dialog.getByRole('button', { name: 'Retry faction view' }).click()
  await expect(dialog.getByLabel('Map navigation')).toBeVisible()
  await expect(dialog.getByRole('alert')).toHaveCount(0)
  await page.keyboard.press('Escape')
  await expect(page.getByRole('button', { name: 'Expand map' })).toBeFocused()
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
