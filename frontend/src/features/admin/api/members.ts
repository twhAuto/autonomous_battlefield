import { createApiClient } from '../../../shared/api/request'

export type MemberRole = 'player' | 'tester'
export interface Member {
  id: string
  email: string
  role: MemberRole
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
  if (response.status === 409) throw new Error('This email has a role set in server configuration, so it is managed outside this page.')
  if (response.status === 404) throw new Error('This member no longer exists. Refresh the list.')
  throw new Error('Member access could not be loaded or changed. Check that member management is configured and try again.')
}
export async function getMembers(signal?: AbortSignal): Promise<Members> {
  const response = await membersFetch('/v1/admin/members', { signal })
  await check(response)
  return response.json() as Promise<Members>
}
// grantMember grants access with role; without one, a new member is a Player and an
// existing member keeps its stored role.
export async function grantMember(email: string, role?: MemberRole) {
  await check(await membersFetch('/v1/admin/members', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(role ? { email, role } : { email }) }))
}
export async function removeMember(id: string) {
  await check(await membersFetch(`/v1/admin/members/${encodeURIComponent(id)}`, { method: 'DELETE' }))
}
export async function retryMembers() {
  await check(await membersFetch('/v1/admin/members/retry', { method: 'POST' }))
}
