const names: Record<string, string> = { arsenal: 'Arsenal', plating: 'Plating', industry: 'Industry' }
// Every ruleset grants 2 per general level, so level N totals 2N.
const trackEffects: Record<string, (total: number) => string> = {
  arsenal: (total) => `+${total} attack for every attacking unit`,
  plating: (total) => `+${total} defense for every unit`,
  industry: (total) => `+${total} gold per tick from each economy building`,
}
const unlocks: Record<string, { label: string; effect: string }> = {
  gunpowder: { label: 'Gunpowder (unlocks Cannon, Gunner)', effect: 'Siege Workshops can train Cannons and Archery Ranges can train Gunners' },
  knighthood: { label: 'Knighthood (unlocks Heavy Cavalry)', effect: 'Stables can train Heavy Cavalry' },
}
// Effects reuse general artwork where they match: attack is the arsenal sword, defense the plating.
const specializations: Record<string, { label: string; target: string; art: string; effect: string }> = {
  vanguard_bulwark: { label: 'Vanguard: Bulwark (+3 defense)', target: 'vanguard', art: 'plating', effect: 'Vanguards gain +3 defense' },
  vanguard_assault: { label: 'Vanguard: Assault (+4 attack)', target: 'vanguard', art: 'arsenal', effect: 'Vanguards gain +4 attack' },
  ranger_longshot: { label: 'Ranger: Longshot (+1 range)', target: 'ranger', art: 'range', effect: 'Rangers attack from 1 hex further' },
  ranger_skirmish: { label: 'Ranger: Skirmish (+1 movement)', target: 'ranger', art: 'movement', effect: 'Rangers move 1 more hex per move' },
  herbalist_potency: { label: 'Herbalist: Potency (+2 treatment)', target: 'herbalist', art: 'treatment', effect: 'Herbalists restore 2 more HP per treatment' },
  herbalist_reach: { label: 'Herbalist: Reach (+1 treatment range)', target: 'herbalist', art: 'range', effect: 'Herbalists treat allies 1 hex further away' },
  siege_mobility: { label: 'Siege: Mobility (+1 movement)', target: 'catapult', art: 'movement', effect: 'Catapults, Battering Rams and Cannons move 1 more hex per move' },
  siege_breach: { label: 'Siege: Breach (+8 building damage)', target: 'catapult', art: 'breach', effect: 'Catapults, Battering Rams and Cannons deal 8 more damage to buildings' },
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
  /** What owning the upgrade does, for hover and disclosure text; absent for unknown IDs. */
  effect?: string
}

function upgradeParts(id: string) {
  const match = /^(arsenal|plating|industry)(?:_([23]))?$/.exec(id)
  return match ? { track: match[1], level: Number(match[2] ?? 1) } : undefined
}

export function upgradePresentation(id: string): UpgradePresentation {
  const upgrade = upgradeParts(id)
  if (upgrade) {
    const level = roman[upgrade.level - 1]
    return { label: `${names[upgrade.track]} ${level}`, art: upgrade.track, level, effect: trackEffects[upgrade.track](2 * upgrade.level) }
  }
  const specialization = specializations[id]
  if (specialization) return { label: specialization.label, art: specialization.art, target: specialization.target, effect: specialization.effect }
  const unlock = unlocks[id]
  if (unlock) return { label: unlock.label, art: id, effect: unlock.effect }
  return { label: id, art: id }
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
