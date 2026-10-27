// Upgrade artwork in the entity icon style. A specialization pairs its unit with its effect.
import { highestOwnedUpgrades, upgradeLabel, upgradePresentation } from '../presentation/upgrades'
import { ArtworkIcon, EntityIcon } from './EntityIcon'

const artwork = new Map<string, string>([
  ['arsenal', 'M12 2 14 5V15H10V5ZM12 6V13M6 15H18M12 15V19M13.5 20.5A1.5 1.5 0 1 0 10.5 20.5 1.5 1.5 0 1 0 13.5 20.5'],
  ['plating', 'M4 5 8 3Q12 7 16 3L20 5 18 10 19 20Q12 23 5 20L6 10ZM12 8V21M7 14Q12 16 17 14'],
  ['industry', 'M18.5 12A6.5 6.5 0 1 0 5.5 12 6.5 6.5 0 1 0 18.5 12M14.5 12A2.5 2.5 0 1 0 9.5 12 2.5 2.5 0 1 0 14.5 12M12 2.5V5.5M12 18.5V21.5M2.5 12H5.5M18.5 12H21.5M5.3 5.3 7.4 7.4M16.6 16.6 18.7 18.7M5.3 18.7 7.4 16.6M16.6 7.4 18.7 5.3'],
  ['gunpowder', 'M7 5H17Q20 13 17 21H7Q4 13 7 5ZM5.9 9H18.1M5.9 17H18.1M12 5C12 2 15 1 17 2M20 1V4M18.5 2.5H21.5'],
  ['knighthood', 'M5 22V8Q5 3 12 3 19 3 19 8V22ZM7 11H17M12 11V19M12 3V1'],
  ['range', 'M19 12A7 7 0 1 0 5 12 7 7 0 1 0 19 12M12 2V8M12 16V22M2 12H8M16 12H22M12 12H12.01'],
  ['movement', 'M7 2H13V12L19 14Q22 15 22 18V21H7ZM7 18H22M1 8H4M1 12H4M2 16H4'],
  ['treatment', 'M9 3H15V9H21V15H15V21H9V15H3V9H9Z'],
  ['breach', 'M2 21V5H22V21ZM2 10H9M15 10H22M2 15H8M16 15H22M6 10V15M18 10V15M12 5 10 10 14 13 11 17 13 21'],
])

export function UpgradeIcon({ id }: Readonly<{ id: string }>) {
  const upgrade = upgradePresentation(id)
  return <span className="upgrade-icon">
    {upgrade.target && <EntityIcon type={upgrade.target} />}
    <ArtworkIcon d={artwork.get(upgrade.art)} />
  </span>
}

export function UpgradeList({ ids }: Readonly<{ ids: string[] }>) {
  return <ul className="upgrade-list">
    {ids.map((id) => <li key={id}><UpgradeIcon id={id} />{upgradeLabel(id)}</li>)}
  </ul>
}

// A faction strip's owned upgrades as one chip: icons and track levels in the summary, names in
// the disclosure, so they are available by hover, keyboard, touch and screen reader like the
// production chips it shares `group` with.
export function UpgradeChips({ upgradeIds, factionName, group }: Readonly<{
  upgradeIds?: string[]; factionName: string; group?: string
}>) {
  const owned = highestOwnedUpgrades(upgradeIds)
  if (!owned.length) return null
  return <details className="upgrade-chips" name={group} aria-label={`${factionName} upgrades`}>
    <summary>
      {owned.map((id) => {
        const { label, level } = upgradePresentation(id)
        return <span key={id} className="upgrade-chips__item" title={label}>
          <UpgradeIcon id={id} />{level && <span className="upgrade-chips__level" aria-hidden="true">{level}</span>}
        </span>
      })}
      <span className="visually-hidden">Upgrades: {owned.map(upgradeLabel).join(', ')}</span>
    </summary>
    <div className="upgrade-chips__detail" aria-hidden="true"><UpgradeList ids={owned} /></div>
  </details>
}
