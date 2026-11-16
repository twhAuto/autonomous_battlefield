// Shared model availability contract used across features.
// aliases are other identifiers that select the option, such as a CLI model alias it replaced.
export interface ModelOption { id: string; aliases?: string[]; reasoningEfforts?: string[]; defaultReasoningEffort?: string }
export interface ModelProfile {
  modelOptions?: ModelOption[]
  id: string; displayName: string; provider?: string; runtime: 'api' | 'cli'; scope?: 'local' | 'cluster'
  modelLabel: string; available: boolean; toolCapability?: boolean; unavailableReason?: string
}
