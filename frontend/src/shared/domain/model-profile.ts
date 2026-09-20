// Shared model availability contract used across features.
export interface ModelProfile {
  id: string; displayName: string; provider?: string; runtime: 'api' | 'cli'; scope?: 'local' | 'cluster'
  modelLabel: string; available: boolean; toolCapability?: boolean; unavailableReason?: string
}
