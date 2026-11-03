import { createApiClient } from '../../../shared/api/request'
import type { ModelProfile } from '../../../shared/domain/model-profile'
export type AccessRole = 'admin' | 'tester' | 'player'
// A model without an entry in `models` allows every role its profile allows.
export interface ModelAccess { enabled: boolean; allowedRoles: AccessRole[]; models?: Record<string, { allowedRoles: AccessRole[] }> }
export interface AccessPolicy { providers: Record<string, boolean>; profiles: Record<string, ModelAccess> }
export interface ModelSettings { version: number; policy: AccessPolicy; models: ModelProfile[] }
const settingsFetch = createApiClient(Error)
export async function getModelSettings(signal?: AbortSignal): Promise<ModelSettings> {
  const response = await settingsFetch('/v1/admin/model-access', { signal })
  if (!response.ok) throw new Error('Model access settings could not be loaded. Try refreshing.')
  return response.json() as Promise<ModelSettings>
}
/** The roles that may use a model: its own entry's roles, never more than its profile allows. */
export function modelRoles(access: ModelAccess, model: string): AccessRole[] {
  return (access.models?.[model]?.allowedRoles ?? access.allowedRoles).filter(role => access.allowedRoles.includes(role))
}
// The server rejects model roles the profile does not allow, so a role removed from a
// profile is also removed from its models when saving.
function savedPolicy(policy: AccessPolicy): AccessPolicy {
  const profiles = Object.fromEntries(Object.entries(policy.profiles).map(([id, access]) => [id, access.models
    ? { ...access, models: Object.fromEntries(Object.keys(access.models).map(model => [model, { allowedRoles: modelRoles(access, model) }])) }
    : access]))
  return { ...policy, profiles }
}
export async function saveModelSettings(settings: ModelSettings): Promise<Pick<ModelSettings, 'version' | 'policy'>> {
  const response = await settingsFetch('/v1/admin/model-access', {
    method: 'PUT', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ version: settings.version, policy: savedPolicy(settings.policy) }),
  })
  if (!response.ok) throw new Error(response.status === 409
    ? 'Another administrator changed these settings. Discard your edits and refresh to load their changes before editing again.'
    : 'Changes could not be saved. Your edits are still here; please try again.')
  return response.json() as Promise<Pick<ModelSettings, 'version' | 'policy'>>
}
