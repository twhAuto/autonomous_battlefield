const names: Record<string, string> = { arsenal: 'Arsenal', plating: 'Plating', industry: 'Industry' }
const specializations: Record<string, string> = {
  vanguard_bulwark: 'Vanguard: Bulwark (+3 defense)', vanguard_assault: 'Vanguard: Assault (+4 attack)',
  ranger_longshot: 'Ranger: Longshot (+1 range)', ranger_skirmish: 'Ranger: Skirmish (+1 movement)',
  herbalist_potency: 'Herbalist: Potency (+2 treatment)', herbalist_reach: 'Herbalist: Reach (+1 treatment range)',
  siege_mobility: 'Siege: Mobility (+1 movement)', siege_breach: 'Siege: Breach (+8 building damage)',
}
const roman = ['I', 'II', 'III']

function upgradeParts(id: string) {
  const match = /^(arsenal|plating|industry)(?:_([23]))?$/.exec(id)
  return match ? { track: match[1], level: Number(match[2] ?? 1) } : undefined
}

export function upgradeLabel(id: string) {
  const upgrade = upgradeParts(id)
  return upgrade ? `${names[upgrade.track]} ${roman[upgrade.level - 1]}` : (specializations[id] ?? id)
}

/** Ownership retains every purchased ID; show only the highest level per track. */
export function ownedUpgradeLabels(ids: string[] = []) {
  const highest = new Map<string, string>()
  for (const id of ids) {
    const upgrade = upgradeParts(id)
    const track = upgrade?.track ?? id
    const previous = highest.get(track)
    if (!previous || (upgrade?.level ?? 0) > (upgradeParts(previous)?.level ?? 0)) highest.set(track, id)
  }
  return [...highest.values()].map(upgradeLabel).join(', ') || 'None'
}
