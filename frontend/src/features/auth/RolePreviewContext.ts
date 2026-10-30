import { createContext } from 'react'
export type PreviewRole = 'tester' | 'player'
export const RolePreviewContext = createContext<{ role: PreviewRole | null; setRole(role: PreviewRole | null): void }>({ role: null, setRole: () => {} })
