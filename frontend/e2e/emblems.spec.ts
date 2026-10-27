import { test, expect, factions } from './fixtures'

test('faction emblems persist across creation, editing, registry, and battle setup', async ({ page, isMobile }) => {
  const saved = factions.map(faction => ({ ...faction, emblem: 'star' }))
  await page.route('**/v1/factions**', async route => {
    const request = route.request()
    if (request.method() === 'POST') {
      saved.push({ ...request.postDataJSON(), ownerId: 'admin' })
      return route.fulfill({ status: 201 })
    }
    if (request.method() === 'PUT') {
      const faction = saved.find(value => request.url().endsWith(value.id))!
      Object.assign(faction, request.postDataJSON())
      return route.fulfill({ status: 204 })
    }
    return route.fulfill({ json: { factions: saved } })
  })
  await page.goto('/factions/new')
  await page.getByLabel('Faction name', { exact: true }).fill('Moonwatch')
  const picker = page.getByRole('group', { name: 'Faction emblem' })
  await expect(picker.getByRole('radio')).toHaveCount(12)
  await page.getByRole('radio', { name: 'Star', exact: true }).focus()
  await page.keyboard.press('ArrowLeft')
  await expect(page.getByRole('radio', { name: 'Moon', exact: true })).toBeChecked()
  await expect(page.getByRole('radio', { name: 'Moon', exact: true })).toBeFocused()
  await picker.screenshot({ path: `/tmp/ab181-emblems-${isMobile ? 'mobile' : 'desktop'}.png` })
  if (isMobile) await page.getByText('Preview faction', { exact: true }).click()
  await expect(page.getByLabel('Faction doctrine preview').locator('.emblem')).toHaveAttribute('data-emblem', 'moon')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.getByRole('button', { name: 'CREATE FACTION' }).click()
  await expect(page.getByText('Moonwatch has entered the battlefield.')).toBeVisible()
  await expect(page.getByRole('radio', { name: 'Star', exact: true })).toBeChecked()
  const id = saved[2].id
  expect(saved[2].emblem).toBe('moon')
  await page.goto('/factions')
  await expect(page.locator('.registry-card').filter({ hasText: 'Moonwatch' }).locator('.emblem')).toHaveAttribute('data-emblem', 'moon')
  await page.goto(`/factions/${id}/edit`)
  await expect(page.getByRole('radio', { name: 'Moon', exact: true })).toBeChecked()
  await page.getByText('Wolf', { exact: true }).click()
  await page.getByRole('button', { name: 'SAVE FACTION' }).click()
  await expect(page.getByText(/Moonwatch has been updated/)).toBeVisible()
  await page.reload()
  await expect(page.getByRole('radio', { name: 'Wolf', exact: true })).toBeChecked()
  await page.goto('/battles/new')
  await page.getByLabel('Slot one', { exact: true }).selectOption(id)
  if (isMobile) await page.getByText('Preview combatants', { exact: true }).click()
  await expect(page.locator('.battle-preview__faction').filter({ hasText: 'Moonwatch' }).locator('.emblem')).toHaveAttribute('data-emblem', 'wolf')
})
