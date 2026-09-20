// Shared unit capability contract used across features.
export interface UnitCapabilities {
  attackMode?: 'tracking' | 'position'
  class?: string; minimumRange?: number; buildingPriority?: boolean
  buildingDamageBonus?: number; bonusAgainstClass?: string; classDamageBonus?: number; healAmount?: number
}
