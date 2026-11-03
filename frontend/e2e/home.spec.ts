import { test, expect } from './fixtures'

test('main menu cards support keyboard navigation to every destination', async ({ page }) => {
  await page.route('**/v1/leagues', route => route.fulfill({ json: { leagues: [] } }))
  await page.route('**/v1/analytics', route => route.fulfill({ json: { battles: 0 } }))
  const destinations = [
    ['Stage battle', '/battles/new', 'Stage a battle'],
    ['Factions', '/factions', 'Factions'],
    ['Battle history', '/battles', 'Battle history'],
    ['Leagues', '/leagues', 'Leagues'],
    ['Statistics', '/analytics', 'Statistics'],
  ]
  await page.goto('/')
  const menu = page.getByRole('region', { name: 'Main menu' })
  // Allow application startup on the shared CI agent before exercising navigation.
  await expect(menu).toBeVisible({ timeout: 15_000 })
  for (const [index, [name, path, heading]] of destinations.entries()) {
    await expect(menu.getByRole('link')).toHaveCount(5)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.locator('#main-content').focus()
    for (let tab = 0; tab <= index; tab++) await page.keyboard.press('Tab')
    await expect(menu.getByRole('link', { name, exact: true })).toBeFocused()
    await expect(menu.getByRole('link', { name, exact: true })).toBeInViewport()
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(path)
    await expect(page.getByRole('heading', { name: heading, exact: true, level: 1 })).toBeVisible()
    await page.getByRole('link', { name: 'OrdinalFrontier home' }).click()
  }
})

test('guest main menu fits every width without a staging card', async ({ page }) => {
  await page.route('**/v1/auth/session', route => route.fulfill({ json: {
    enabled: true, authenticated: true, mode: 'cloudflare', actor: { id: 'guest', role: 'guest' },
  } }))
  for (const width of [320, 768, 1024, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/')
    const menu = page.getByRole('region', { name: 'Main menu' })
    await expect(menu).toBeVisible({ timeout: 15_000 })
    await expect(menu.getByRole('link')).toHaveText([
      /Factions/, /Battle history/, /Leagues/, /Statistics/,
    ])
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  }
})
