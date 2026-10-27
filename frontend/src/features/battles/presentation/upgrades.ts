const names: Record<string, string> = { arsenal: 'Arsenal', plating: 'Plating', industry: 'Industry' }
const unlocks: Record<string, string> = {
  gunpowder: 'Gunpowder (unlocks Cannon, Gunner)', knighthood: 'Knighthood (unlocks Heavy Cavalry)',
}
// Effects reuse general artwork where they match: attack is the arsenal sword, defense the plating.
const specializations: Record<string, { label: string; target: string; effect: string }> = {
  vanguard_bulwark: { label: 'Vanguard: Bulwark (+3 defense)', target: 'vanguard', effect: 'plating' },
  vanguard_assault: { label: 'Vanguard: Assault (+4 attack)', target: 'vanguard', effect: 'arsenal' },
  ranger_longshot: { label: 'Ranger: Longshot (+1 range)', target: 'ranger', effect: 'range' },
  ranger_skirmish: { label: 'Ranger: Skirmish (+1 movement)', target: 'ranger', effect: 'movement' },
  herbalist_potency: { label: 'Herbalist: Potency (+2 treatment)', target: 'herbalist', effect: 'treatment' },
  herbalist_reach: { label: 'Herbalist: Reach (+1 treatment range)', target: 'herbalist', effect: 'range' },
  siege_mobility: { label: 'Siege: Mobility (+1 movement)', target: 'catapult', effect: 'movement' },
  siege_breach: { label: 'Siege: Breach (+8 building damage)', target: 'catapult', effect: 'breach' },
}
const roman = ['I', 'II', 'III']

export interface UpgradePresentation {
  label: string
  /** Upgrade artwork key; unknown keys draw the fallback glyph. */
  art: string
  /** General track level, shown beside the icon where the full label does not fit. */
  level?: string
  /** Unit type drawn before a specialization's effect. */
  target?: string
}

function upgradeParts(id: string) {
  const match = /^(arsenal|plating|industry)(?:_([23]))?$/.exec(id)
  return match ? { track: match[1], level: Number(match[2] ?? 1) } : undefined
}

export function upgradePresentation(id: string): UpgradePresentation {
  const upgrade = upgradeParts(id)
  if (upgrade) {
    const level = roman[upgrade.level - 1]
    return { label: `${names[upgrade.track]} ${level}`, art: upgrade.track, level }
  }
  const specialization = specializations[id]
  if (specialization) return { label: specialization.label, art: specialization.effect, target: specialization.target }
  return { label: unlocks[id] ?? id, art: id }
}

export function upgradeLabel(id: string) {
  return upgradePresentation(id).label
}

/** Ownership retains every purchased ID; keep only the highest level per track, in first-purchase order. */
export function highestOwnedUpgrades(ids: readonly string[] = []) {
  const highest = new Map<string, string>()
  for (const id of ids) {
    const upgrade = upgradeParts(id)
    const track = upgrade?.track ?? id
    const previous = highest.get(track)
    if (!previous || (upgrade?.level ?? 0) > (upgradeParts(previous)?.level ?? 0)) highest.set(track, id)
  }
  return [...highest.values()]
}
