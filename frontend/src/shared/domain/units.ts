// Shared unit capability contract used across features.
export interface UnitCapabilities {
  scout?: boolean; preferredTargetClass?: string; armorPiercing?: number
  repairAmount?: number; repairCost?: number; repairCharges?: number; healRange?: number
  attackMode?: 'tracking' | 'position'
  class?: string; minimumRange?: number; buildingPriority?: boolean
  buildingDamageBonus?: number; bonusAgainstClass?: string; classDamageBonus?: number; healAmount?: number
}
