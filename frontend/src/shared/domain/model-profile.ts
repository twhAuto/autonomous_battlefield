// Shared model availability contract used across features.
export interface ModelOption { id: string; reasoningEfforts?: string[]; defaultReasoningEffort?: string }
export interface ModelProfile {
  modelOptions?: ModelOption[]
  id: string; displayName: string; provider?: string; runtime: 'api' | 'cli'; scope?: 'local' | 'cluster'
  modelLabel: string; available: boolean; toolCapability?: boolean; unavailableReason?: string
}
