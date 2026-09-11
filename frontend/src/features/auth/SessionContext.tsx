import { createContext, useContext } from 'react'
import { RolePreviewContext } from './RolePreviewContext'
import type { AuthSession } from './api/auth'
// Standalone development components use the local administrator; App always supplies a session.
export const SessionContext = createContext<AuthSession>({ enabled: true, authenticated: true, actor: { id: 'local-development', role: 'admin' } })
export function usePermissions() {
 const session = useContext(SessionContext)
 const preview = useContext(RolePreviewContext)
 const role = session.actor?.role ?? (session.mode === 'cloudflare' ? 'player' : 'admin')
 return { inspect: role === 'admin' || role === 'developer', canManage: (owner?: string) => !preview.role && (role === 'admin' || (!!owner && owner === session.actor?.id)) }
}
