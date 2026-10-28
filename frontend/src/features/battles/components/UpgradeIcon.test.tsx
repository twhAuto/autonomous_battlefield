import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'
import { EntityIcon } from './EntityIcon'
import { UpgradeChips, UpgradeIcon, UpgradeList } from './UpgradeIcon'

const catalog = ['arsenal', 'plating', 'industry', 'gunpowder', 'knighthood',
  'vanguard_bulwark', 'vanguard_assault', 'ranger_longshot', 'ranger_skirmish',
  'herbalist_potency', 'herbalist_reach', 'siege_mobility', 'siege_breach']

const artwork = (id: string) => {
  const { container, unmount } = render(<UpgradeIcon id={id} />)
  const paths = [...container.querySelectorAll('path')].map((path) => path.getAttribute('d')).join('|')
  unmount()
  return paths
}

it('draws distinct artwork for every catalog upgrade and the fallback for unknown IDs', () => {
  const drawn = catalog.map(artwork)
  expect(drawn).not.toContain(artwork('not-an-upgrade'))
  expect(new Set(drawn).size).toBe(drawn.length)
  expect(artwork('arsenal_3')).toBe(artwork('arsenal'))
})

it('pairs a specialization with the unit it improves', () => {
  const unit = render(<EntityIcon type="ranger" />)
  const ranger = unit.container.querySelector('path')!.getAttribute('d')
  unit.unmount()
  const { container } = render(<UpgradeIcon id="ranger_longshot" />)
  const paths = [...container.querySelectorAll('path')].map((path) => path.getAttribute('d'))
  expect(paths).toHaveLength(2)
  expect(paths[0]).toBe(ranger)
  expect(artwork('arsenal')).toBe(artwork('vanguard_assault').split('|')[1])
})

it('lists upgrades with icons, readable names and what each does', () => {
  render(<UpgradeList ids={['industry_2', 'knighthood', 'legacy_custom']} />)
  const items = screen.getAllByRole('listitem')
  expect(items.map((item) => [item.lastElementChild!.firstChild?.textContent, item.querySelector('small')?.textContent])).toEqual([
    ['Industry II', '+4 gold per tick from each economy building'],
    ['Knighthood (unlocks Heavy Cavalry)', 'Stables can train Heavy Cavalry'],
    ['legacy_custom', undefined],
  ])
  items.forEach((item) => expect(item.querySelector('svg')).toBeInTheDocument())
})

it('shows the highest level per track in one disclosure that shares its group', async () => {
  const user = userEvent.setup()
  render(<UpgradeChips upgradeIds={['arsenal', 'gunpowder', 'arsenal_2', 'vanguard_assault']} factionName="Amber" group="g" />)
  const chip = screen.getByRole('group', { name: 'Amber upgrades' })
  expect(chip).toHaveAttribute('name', 'g')
  const summary = chip.querySelector('summary')!
  expect(summary).toHaveTextContent('Upgrades: Arsenal II: +4 attack for every attacking unit; Gunpowder (unlocks Cannon, Gunner): Siege Workshops can train Cannons and Archery Ranges can train Gunners; Vanguard: Assault (+4 attack): Vanguards gain +4 attack')
  // Hovering an icon reveals its name and effect; the level numeral stays beside the icon.
  expect([...summary.querySelectorAll('.upgrade-chips__item')].map((item) => [
    item.querySelector('.upgrade-chips__level')?.textContent, item.querySelector('.upgrade-chips__tooltip')?.textContent,
  ])).toEqual([
    ['II', 'Arsenal II+4 attack for every attacking unit'],
    [undefined, 'Gunpowder (unlocks Cannon, Gunner)Siege Workshops can train Cannons and Archery Ranges can train Gunners'],
    [undefined, 'Vanguard: Assault (+4 attack)Vanguards gain +4 attack'],
  ])
  expect(chip).not.toHaveAttribute('open')
  await user.click(summary)
  expect(chip).toHaveAttribute('open')
  expect(within(chip.querySelector<HTMLElement>('.upgrade-chips__detail')!).getAllByRole('listitem', { hidden: true }).map((item) => item.querySelector('small')?.textContent))
    .toEqual(['+4 attack for every attacking unit', 'Siege Workshops can train Cannons and Archery Ranges can train Gunners', 'Vanguards gain +4 attack'])
})

it('renders nothing before any upgrade is owned', () => {
  const { container } = render(<UpgradeChips factionName="Amber" />)
  expect(container).toBeEmptyDOMElement()
})
