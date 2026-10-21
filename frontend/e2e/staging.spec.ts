import { test, expect, factionId, rivalId } from './fixtures'

test('stages a battle end to end and opens the queued engagement', async ({ page, api, isMobile }) => {
  await page.goto('/battles/new')
  await expect(page.getByRole('heading', { name: 'Stage a battle' })).toBeVisible()
  await expect(page.getByLabel('Combat map')).toHaveValue('castle-siege')
  // Both slots prefill from the faction directory, and each excludes the other's choice.
  await expect(page.getByLabel('Slot one')).toHaveValue(factionId)
  await expect(page.getByLabel('Slot two')).toHaveValue(rivalId)

  const ticks = page.getByLabel('Maximum ticks')
  await ticks.fill('801')
  await expect(page.getByText('Enter a whole number from 1 to 800.')).toBeVisible()
  await expect(page.getByRole('button', { name: /DEPLOY COMBATANTS/ })).toBeDisabled()
  await ticks.fill('120')
  await expect(page.getByRole('button', { name: /DEPLOY COMBATANTS/ })).toBeEnabled()

  if (isMobile) await expect(page.getByLabel('Selected combatants')).not.toBeVisible()
  await page.getByRole('button', { name: /DEPLOY COMBATANTS/ }).click()

  // The staged battle is submitted and the operator lands on its live view.
  await expect(page).toHaveURL(/\/battles\/[0-9a-f-]{36}$/)
  await expect(page.getByRole('progressbar', { name: 'Tick limit used' })).toBeVisible()
  expect(api.writes).toEqual(['POST /v1/battles'])
  expect(api.created).toMatchObject({
    scenarioId: 'castle-siege',
    tickCount: 120,
    participants: [
      { factionId, controller: { type: 'scripted' } },
      { factionId: rivalId, controller: { type: 'scripted' } },
    ],
  })
  expect(api.created?.id).toMatch(/^[0-9a-f-]{36}$/)
})

test('keeps the operator on the form when the server rejects the battle', async ({ page, api }) => {
  api.createFails = true
  await page.goto('/battles/new')
  await page.getByRole('button', { name: /DEPLOY COMBATANTS/ }).click()
  await expect(page.getByText('Both factions must use the scenario ruleset.')).toBeVisible()
  await expect(page).toHaveURL(/\/battles\/new$/)
  expect(api.created).toBeNull()

  // A retry after the server recovers proceeds to the battle view.
  api.createFails = false
  await page.getByRole('button', { name: /DEPLOY COMBATANTS/ }).click()
  await expect(page).toHaveURL(/\/battles\/[0-9a-f-]{36}$/)
  expect(api.created?.tickCount).toBe(500)
})
