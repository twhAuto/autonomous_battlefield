// Stable profile identifiers; keep aligned with faction service validation and migration 00021.
export const factionEmblems = [
  { id: 'star', name: 'Star' },
  { id: 'crown', name: 'Crown' },
  { id: 'shield', name: 'Shield' },
  { id: 'swords', name: 'Crossed swords' },
  { id: 'wolf', name: 'Wolf' },
  { id: 'raven', name: 'Raven' },
  { id: 'serpent', name: 'Serpent' },
  { id: 'tree', name: 'Tree' },
  { id: 'leaf', name: 'Leaf' },
  { id: 'flame', name: 'Flame' },
  { id: 'sun', name: 'Sun' },
  { id: 'moon', name: 'Moon' },
] as const

export type FactionEmblem = typeof factionEmblems[number]['id']

export function resolveEmblem(value?: string): FactionEmblem {
  return factionEmblems.find(emblem => emblem.id === value)?.id ?? 'star'
}
