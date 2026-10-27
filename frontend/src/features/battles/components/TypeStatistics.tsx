// After-action detail for one faction: what it fielded and lost per unit and building
// type, and when it bought each upgrade. Results from before per-type statistics have
// none of these lists, so nothing renders for them.
import type { FactionStatistics } from '../api/battles'
import { typeLabel } from '../presentation/entities'
import { upgradeLabel } from '../presentation/upgrades'
import { EntityIcon } from './EntityIcon'
import { UpgradeIcon } from './UpgradeIcon'

export function TypeStatistics({ stats, factionName }: Readonly<{ stats: FactionStatistics; factionName: string }>) {
  const units = stats.unitTypes ?? []
  const buildings = stats.buildingTypes ?? []
  const upgrades = stats.upgrades ?? []
  if (!units.length && !buildings.length && !upgrades.length) return null
  const healers = units.some((unit) => unit.healthRestored > 0)
  return (
    <div className="type-statistics">
      {units.length > 0 && (
        <div className="type-statistics__scroll">
          <table>
            <caption>{factionName} units by type</caption>
            <thead><tr>
              <th scope="col">Unit</th><th scope="col">Fielded</th><th scope="col">Lost</th><th scope="col">Kills</th>
              <th scope="col">Damage</th>{healers && <th scope="col">Healed</th>}<th scope="col">Gold</th>
            </tr></thead>
            <tbody>
              {units.map((unit) => (
                <tr key={unit.unitType}>
                  <th scope="row"><EntityIcon type={unit.unitType} />{typeLabel(unit.unitType)}</th>
                  <td>{unit.started + unit.recruited}</td><td>{unit.lost}</td><td>{unit.kills}</td>
                  <td>{unit.damageDealt}</td>{healers && <td>{unit.healthRestored}</td>}<td>{unit.goldSpent}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {buildings.length > 0 && (
        <div className="type-statistics__scroll">
          <table>
            <caption>{factionName} buildings by type</caption>
            <thead><tr>
              <th scope="col">Building</th><th scope="col">Built</th><th scope="col">Lost</th><th scope="col">Kills</th><th scope="col">Gold</th>
            </tr></thead>
            <tbody>
              {buildings.map((building) => (
                <tr key={building.buildingType}>
                  <th scope="row"><EntityIcon type={building.buildingType} />{typeLabel(building.buildingType)}</th>
                  <td>{building.constructed}</td><td>{building.lost}</td><td>{building.kills}</td><td>{building.goldSpent}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {upgrades.length > 0 && (
        <ol className="upgrade-timeline" aria-label={`${factionName} upgrade timeline`}>
          {upgrades.map((upgrade) => (
            <li key={upgrade.upgradeId}><UpgradeIcon id={upgrade.upgradeId} />{upgradeLabel(upgrade.upgradeId)}<span>TICK {upgrade.tick}</span></li>
          ))}
        </ol>
      )}
    </div>
  )
}
