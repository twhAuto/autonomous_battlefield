import { test, expect } from './fixtures'

test('admin model access saves and role preview works on desktop and mobile', async ({ page }) => {
  const model = { id: 'api', displayName: 'OpenAI API', modelLabel: 'Configured model', provider: 'openai', runtime: 'api', scope: 'cluster', available: true }
  let policy = { providers: { openai: true }, profiles: { api: { enabled: true, allowedRoles: ['admin', 'tester', 'player'] } } }
  let version = 0
  await page.route('**/v1/admin/model-access', async route => {
    if (route.request().method() === 'PUT') { policy = route.request().postDataJSON().policy; version++; return route.fulfill({ json: { policy, version } }) }
    return route.fulfill({ json: { models: [model], policy, version } })
  })
  await page.route('**/v1/model-profiles', async route => route.fulfill({ json: { profiles: policy.providers.openai && policy.profiles.api.enabled ? [model] : [] } }))
  await page.goto('/admin')
  await expect(page.getByRole('heading', { name: 'Models & access' })).toBeVisible()
  await page.getByRole('switch', { name: 'openai provider access' }).click()
  await expect(page.getByRole('switch', { name: 'OpenAI API model access' })).toBeDisabled()
  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(page.getByText('Changes saved', { exact: true })).toBeVisible()
  await page.reload()
  await expect(page.getByRole('switch', { name: 'openai provider access' })).not.toBeChecked()
  await page.getByRole('switch', { name: 'openai provider access' }).click()
  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(page.getByText('Changes saved', { exact: true })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  const previewRead = page.waitForRequest(request => request.url().includes('/v1/model-profiles') && request.headers()['x-battlefield-preview-role'] === 'player')
  await page.getByRole('button', { name: 'View as Player' }).click()
  await previewRead
  await expect(page.getByRole('status')).toContainText('Viewing as Player')
  await expect(page.getByRole('link', { name: 'Admin', exact: true })).toHaveCount(0)
  await page.getByRole('link', { name: 'OrdinalFrontier home' }).click()
  const menu = page.getByRole('region', { name: 'Main menu' })
  await expect(menu.getByRole('link')).toHaveCount(5)
  await expect(menu.getByRole('link', { name: 'Leagues' })).toBeVisible()
  await expect(menu.getByRole('link', { name: 'Statistics' })).toBeVisible()
  await page.getByRole('button', { name: 'Exit preview' }).click()
  await expect(menu.getByRole('link')).toHaveCount(5)
  if (await page.getByRole('button', { name: 'Menu', exact: true }).isVisible()) await page.getByRole('button', { name: 'Menu', exact: true }).click()
  await expect(page.getByRole('link', { name: 'Admin', exact: true })).toBeVisible()
})
