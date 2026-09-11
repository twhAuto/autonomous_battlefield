import { createApiClient } from '../../../shared/api/request'
import type { ModelProfile } from '../../battles/api/battles'
export type AccessRole = 'admin' | 'developer' | 'player'
export interface ModelAccess { enabled: boolean; allowedRoles: AccessRole[] }
export interface AccessPolicy { providers: Record<string, boolean>; profiles: Record<string, ModelAccess> }
export interface ModelSettings { version: number; policy: AccessPolicy; models: ModelProfile[] }
const settingsFetch = createApiClient(Error)
export async function getModelSettings(signal?: AbortSignal): Promise<ModelSettings> {
  const response = await settingsFetch('/v1/admin/model-access', { signal })
  if (!response.ok) throw new Error('Model access settings could not be loaded. Try refreshing.')
  return response.json() as Promise<ModelSettings>
}
export async function saveModelSettings(settings: ModelSettings): Promise<Pick<ModelSettings, 'version' | 'policy'>> {
  const response = await settingsFetch('/v1/admin/model-access', {
    method: 'PUT', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ version: settings.version, policy: settings.policy }),
  })
  if (!response.ok) throw new Error(response.status === 409
    ? 'Another administrator changed these settings. Discard your edits and refresh to load their changes before editing again.'
    : 'Changes could not be saved. Your edits are still here; please try again.')
  return response.json() as Promise<Pick<ModelSettings, 'version' | 'policy'>>
}
