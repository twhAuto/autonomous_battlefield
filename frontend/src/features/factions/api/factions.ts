import { apiFetch, createApiClient } from '../../../shared/api/request'

export interface CreateFactionInput {
  id: string
  name: string
  rulesetId: string
  blueprint: FactionBlueprint
}

export interface UnitSelection { type: string; count: number }
export interface FactionBlueprint {
  units: UnitSelection[]
  upgradeIds: string[]
  recruitmentPriority: string[]
}

export interface UnitRule {
  id: string; cost: number; health: number; attack: number; defense: number
  speed: number; movement: number; range: number; vision: number
}
export interface UpgradeRule {
  id: string; cost: number; attackBonus: number; defenseBonus: number; economyIncomeBonus: number
}
export interface FactionRuleset {
  id: string; version: string; designBudget: number; startingGold: number
  units: UnitRule[]; upgrades: UpgradeRule[]
}

export interface Faction {
  id: string
  name: string
  rulesetId: string
  blueprint: FactionBlueprint
  spentPoints: number
  remainingPoints: number
}

export async function listFactionRulesets(signal?: AbortSignal): Promise<FactionRuleset[]> {
  const response = await apiFetch(`/v1/faction-rulesets`, { signal })
  if (!response.ok) throw new FactionApiError('Faction design rules could not be loaded.', response.status)
  const body = (await response.json()) as { rulesets: FactionRuleset[] }
  return body.rulesets
}

export class FactionApiError extends Error {
  constructor(
    message: string,
    public readonly status?: number,
  ) {
    super(message)
    this.name = 'FactionApiError'
  }
}

const factionFetch = createApiClient(FactionApiError)

export async function listFactions(signal?: AbortSignal): Promise<Faction[]> {
  const response = await factionFetch(`/v1/factions`, { signal })

  if (!response.ok) {
    throw new FactionApiError(
      'The faction registry could not be loaded. Please try again in a moment.',
      response.status,
    )
  }

  const body = (await response.json()) as { factions: Faction[] }
  // Go encodes an empty optional upgrade slice as null. Keep the UI model an array.
  return body.factions.map(faction => faction.blueprint ? {
    ...faction,
    blueprint: { ...faction.blueprint, upgradeIds: faction.blueprint.upgradeIds ?? [] },
  } : faction)
}

export async function createFaction(
  faction: CreateFactionInput,
): Promise<void> {
  const response = await factionFetch(`/v1/factions`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(faction),
    })

  if (response.ok) return

  if (response.status === 409) {
    throw new FactionApiError(
      'That faction identifier is already in use. Generate a new one and try again.',
      response.status,
    )
  }

  if (response.status === 400) {
    throw new FactionApiError(
      'The server rejected these faction details. Review the name and try again.',
      response.status,
    )
  }

  if (response.status === 422) {
    const detail = (await response.text()).trim()
    throw new FactionApiError(
      detail || 'This blueprint is illegal or exceeds its design budget.',
      response.status,
    )
  }

  throw new FactionApiError(
    'The faction could not be created. Please try again in a moment.',
    response.status,
  )
}

export async function deleteFaction(id: string): Promise<void> {
	const response = await factionFetch(`/v1/factions/${encodeURIComponent(id)}`, { method: 'DELETE' })
	if (response.ok) return
	if (response.status === 404) {
		throw new FactionApiError('That faction no longer exists.', response.status)
	}
	if (response.status === 400) {
		throw new FactionApiError('The faction identifier is invalid.', response.status)
	}
	throw new FactionApiError('The faction could not be deleted. Please try again.', response.status)
}
