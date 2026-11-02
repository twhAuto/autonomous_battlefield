import { createContext, useContext } from 'react'
import { RolePreviewContext } from './RolePreviewContext'
import type { AuthSession } from './api/auth'
// Standalone development components use the local administrator; App always supplies a session.
export const SessionContext = createContext<AuthSession>({ enabled: true, authenticated: true, actor: { id: 'local-development', role: 'admin' } })
export function usePermissions() {
 const session = useContext(SessionContext)
 const preview = useContext(RolePreviewContext)
 const role = session.actor?.role ?? (session.mode === 'cloudflare' ? 'player' : 'admin')
 // Guests only view: they start, create and manage nothing.
 const play = role !== 'guest'
 const actorId = session.actor?.id
 // A Player may create a faction only while owning none; the API enforces the same rule.
 const oneFaction = role === 'player'
 return {
  inspect: role === 'admin' || role === 'tester', play, oneFaction,
  canManage: (owner?: string) => !preview.role && play && (role === 'admin' || (!!owner && owner === actorId)),
  atFactionLimit: (factions: readonly { ownerId?: string }[]) => oneFaction && !!actorId && factions.some(faction => faction.ownerId === actorId),
 }
}
