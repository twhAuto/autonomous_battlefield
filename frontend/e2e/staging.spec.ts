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
  else await expect(page.getByRole('img', { name: 'castle-siege layout, 15 by 11 hexes' })).toBeVisible()
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

test('aligns mixed controller slots and submits chosen timing', async ({ page, api, isMobile }) => {
  await page.route('**/v1/model-profiles', route => route.fulfill({ json: { profiles: [{ id: 'model', displayName: 'Model controller', available: true, runtime: 'cli', modelLabel: 'fast', modelOptions: [{ id: 'fast', reasoningEfforts: ['low', 'high'] }] }] } }))
  await page.goto('/battles/new')
  await page.locator('#first-controller').selectOption('model')
  await expect(page.getByLabel('Reasoning effort')).toHaveValue('low')
  const first = await page.getByLabel('Slot one').boundingBox()
  const second = await page.getByLabel('Slot two').boundingBox()
  expect(first).not.toBeNull()
  expect(second).not.toBeNull()
  if (isMobile) {
    expect(second!.y).toBeGreaterThan(first!.y + first!.height)
  } else {
    expect(Math.abs(first!.y - second!.y)).toBeLessThan(2)
    const firstController = await page.locator('#first-controller').boundingBox()
    const secondController = await page.locator('#second-controller').boundingBox()
    expect(Math.abs(firstController!.y - secondController!.y)).toBeLessThan(2)
  }
  await expect(page.getByText('AI integration status')).toHaveCount(0)
  await expect(page.getByText(/Units:|Buildings:|Three upgrade levels/)).toHaveCount(0)
  await page.getByLabel('Tick duration').selectOption('500ms')
  await page.getByLabel('Decision interval').selectOption('5')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({ path: `/tmp/ab169-setup-${isMobile ? 'mobile' : 'desktop'}.png`, fullPage: true })
  await page.getByRole('button', { name: /DEPLOY COMBATANTS/ }).click()
  await expect(page).toHaveURL(/\/battles\/[0-9a-f-]{36}$/)
  expect(api.created).toMatchObject({ tickDuration: '500ms', decisionIntervalTicks: 5, participants: [{ controller: { type: 'model', profileId: 'model', modelIdentifier: 'fast', reasoningEffort: 'low' } }, { controller: { type: 'scripted' } }] })
})

test('puts plan and rationale inside each faction status box', async ({ page, isMobile }) => {
  await page.route('**/v1/battles/*/decisions?*', route => route.fulfill({ json: { decisions: [
    { requestId: 'a', factionId, targetTick: 80, status: 'accepted', controllerType: 'model', usage: {}, createdAt: '', resolvedPlan: { name: 'Defend the castle', rationale: 'Hold the bridge while the economy recovers. Reinforce the western approach before advancing.' } },
    { requestId: 'b', factionId: rivalId, targetTick: 80, status: 'fallback', controllerType: 'scripted', usage: {}, createdAt: '', resolvedPlan: { name: 'Advance', rationale: 'Keep pressure on the enemy castle.' } },
  ] } }))
  await page.goto('/battles/33333333-3333-4333-8333-333333333333')
  const amber = page.getByRole('article', { name: 'Amber', exact: true })
  const azure = page.getByRole('article', { name: 'Azure', exact: true })
  await expect(amber.locator('summary')).toContainText('Defend the castle')
  await expect(azure.locator('summary')).toContainText('fallback')
  await amber.locator('summary').click()
  await expect(amber.getByRole('textbox')).toHaveValue(/Hold the bridge/)
  await amber.locator('summary').click()
  await azure.locator('summary').click()
  await expect(azure.getByRole('textbox')).toHaveValue(/Keep pressure on the enemy castle/)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({ path: `/tmp/ab169-battle-${isMobile ? 'mobile' : 'desktop'}.png`, fullPage: true })
})

test('keeps long plan text inside a bounded keyboard-accessible area', async ({ page }) => {
  await page.route('**/v1/battles/*/decisions?*', route => route.fulfill({ json: { decisions: [
    { requestId: 'long', factionId, targetTick: 80, status: 'accepted', controllerType: 'model', usage: {}, createdAt: '', resolvedPlan: { name: 'Hold '.repeat(200), rationale: 'Defend.\n'.repeat(70) } },
  ] } }))
  await page.goto('/battles/33333333-3333-4333-8333-333333333333')
  await page.getByRole('article', { name: 'Amber', exact: true }).locator('summary').click()
  const plan = page.getByRole('textbox', { name: 'Amber plan and rationale', exact: true })
  await expect(plan).toBeVisible()
  await expect(plan).toHaveAttribute('readonly', '')
  await expect(plan).toHaveValue(/Defend\./)
  await plan.focus()
  await expect(plan).toBeFocused()
  const bounds = await plan.evaluate(node => ({ height: node.getBoundingClientRect().height, visible: node.clientHeight, total: node.scrollHeight }))
  expect(bounds.height).toBeLessThanOrEqual(320)
  expect(bounds.total).toBeGreaterThan(bounds.visible)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
})
