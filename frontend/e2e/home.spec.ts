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
  for (const [index, [name, path, heading]] of destinations.entries()) {
    await page.goto('/')
    const menu = page.getByRole('region', { name: 'Main menu' })
    await expect(menu.getByRole('link')).toHaveCount(5)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.locator('#main-content').focus()
    for (let tab = 0; tab <= index; tab++) await page.keyboard.press('Tab')
    await expect(menu.getByRole('link', { name, exact: true })).toBeFocused()
    await expect(menu.getByRole('link', { name, exact: true })).toBeInViewport()
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(path)
    await expect(page.getByRole('heading', { name: heading, exact: true, level: 1 })).toBeVisible()
  }
})

test('player main menu stays compact at narrow widths', async ({ page }) => {
  await page.route('**/v1/auth/session', route => route.fulfill({ json: {
    enabled: true, authenticated: true, mode: 'cloudflare', actor: { id: 'player', role: 'player' },
  } }))
  for (const width of [320, 768, 1024]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/')
    const menu = page.getByRole('region', { name: 'Main menu' })
    await expect(menu.getByRole('link')).toHaveText([
      /Stage battle/, /Factions/, /Battle history/,
    ])
    await expect(menu.getByRole('link', { name: 'Leagues' })).toHaveCount(0)
    await expect(menu.getByRole('link', { name: 'Statistics' })).toHaveCount(0)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  }
})
