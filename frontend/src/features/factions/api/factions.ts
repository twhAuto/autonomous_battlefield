// Faction profile and ruleset API types and requests, with shared transport and feature-
// specific error messages.

import type { UnitCapabilities } from '../../../shared/domain/units'
import { createApiClient, errorMessage } from '../../../shared/api/request'

export interface CreateFactionInput {
  id: string
  name: string
  doctrine: string
  background?: string
}

export interface UnitRule extends UnitCapabilities {
  id: string; cost: number; health: number; attack: number; defense: number
  speed: number; movement: number; range: number; vision: number
}
export interface UpgradeRule {
  id: string; cost: number; attackBonus: number; defenseBonus: number; economyIncomeBonus: number
}
export interface FactionRuleset {
  id: string; version: string; startingGold: number
  units: UnitRule[]; upgrades: UpgradeRule[]
}

export interface Faction {
  ownerId?: string
  id: string
  name: string
  doctrine: string
  background?: string
}

export async function listFactionRulesets(signal?: AbortSignal): Promise<FactionRuleset[]> {
  const response = await factionFetch(`/v1/faction-rulesets`, { signal })
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
  return body.factions
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
      'The server rejected these faction details. Review the name and doctrine and try again.',
      response.status,
    )
  }

  if (response.status === 422) {
    const detail = await errorMessage(response)
    throw new FactionApiError(
      detail || 'These faction details are invalid.',
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

export async function updateFaction(faction: CreateFactionInput): Promise<void> {
 const response = await factionFetch(`/v1/factions/${encodeURIComponent(faction.id)}`, {
 method: 'PUT', headers: { 'Content-Type': 'application/json' },
 body: JSON.stringify({ name: faction.name, doctrine: faction.doctrine, background: faction.background ?? '' }),
 })
 if (!response.ok) throw new FactionApiError(response.status === 404 ? 'Faction not found.' : 'The faction could not be updated. Check the name and doctrine and try again.', response.status)
}
