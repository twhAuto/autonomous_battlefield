import { createApiClient } from '../../../shared/api/request'

export interface Member {
  id: string
  email: string
  desiredAccess: boolean
  version: number
  syncedVersion: number
  updatedAt: string
}
export interface Members { members: Member[]; syncError: string; gameUrl: string }
const membersFetch = createApiClient(Error)
async function check(response: Response) {
  if (response.ok) return
  if (response.status === 400) throw new Error('Enter a valid email address.')
  if (response.status === 409) throw new Error('Administrator and tester access is managed outside this page.')
  if (response.status === 404) throw new Error('This member no longer exists. Refresh the list.')
  throw new Error('Player access could not be loaded or changed. Check that member management is configured and try again.')
}
export async function getMembers(signal?: AbortSignal): Promise<Members> {
  const response = await membersFetch('/v1/admin/members', { signal })
  await check(response)
  return response.json() as Promise<Members>
}
export async function grantMember(email: string) {
  await check(await membersFetch('/v1/admin/members', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email }) }))
}
export async function removeMember(id: string) {
  await check(await membersFetch(`/v1/admin/members/${encodeURIComponent(id)}`, { method: 'DELETE' }))
}
export async function retryMembers() {
  await check(await membersFetch('/v1/admin/members/retry', { method: 'POST' }))
}
