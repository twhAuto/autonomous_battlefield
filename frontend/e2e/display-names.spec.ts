import { test, expect, battleId } from './fixtures'

test('a new player chooses a display name before entering', async ({ page, api, isMobile }) => {
  api.session = { enabled: true, authenticated: true, mode: 'cloudflare', actor: { id: 'issuer|new', role: 'player' } }
  api.takenNames = ['iron duke']
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Choose your display name' })).toBeVisible({ timeout: 15_000 })
  await expect(page.getByRole('navigation')).toHaveCount(0)
  const name = page.getByLabel('Display name', { exact: true })
  await name.fill('Iron Duke')
  await page.getByRole('button', { name: /ENTER COMMAND POST/ }).click()
  await expect(page.getByRole('alert')).toHaveText('That name is taken. Choose another.')
  await name.fill('  Duke of Iron ')
  await page.getByRole('button', { name: /ENTER COMMAND POST/ }).click()
  await expect(page.getByRole('heading', { name: 'Main menu' })).toBeVisible()
  expect(api.writes.filter(write => write === 'PUT /v1/profile')).toHaveLength(2)
  if (isMobile) await page.getByRole('button', { name: 'Menu', exact: true }).click()
  await expect(page.getByRole('link', { name: 'Signed in as Duke of Iron' }).filter({ visible: true })).toHaveAttribute('href', '/profile')
  // The saved name survives a reload.
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Main menu' })).toBeVisible()
})

test('a guest sees who started battles and created factions', async ({ page, api, isMobile }) => {
  api.session = { enabled: true, authenticated: true, mode: 'cloudflare', actor: { id: 'issuer|visitor', role: 'guest' } }
  await page.goto('/factions')
  await expect(page.getByText('Created by Admin')).toHaveCount(2)
  if (isMobile) await page.getByRole('button', { name: 'Menu', exact: true }).click()
  await expect(page.getByText('Signed in as Guest').filter({ visible: true })).toBeVisible()
  await page.goto('/battles')
  await expect(page.getByText('Started by Admin')).toBeVisible()
  await page.goto(`/battles/${battleId}`)
  await expect(page.getByLabel('Battle status').getByText('Started by Admin')).toBeVisible()
})
