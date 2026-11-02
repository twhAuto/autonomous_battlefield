import { createContext } from 'react'
export type PreviewRole = 'tester' | 'player' | 'guest'
export const previewRoleNames: Record<PreviewRole, string> = { tester: 'Tester', player: 'Player', guest: 'Guest' }
export const RolePreviewContext = createContext<{ role: PreviewRole | null; setRole(role: PreviewRole | null): void }>({ role: null, setRole: () => {} })
