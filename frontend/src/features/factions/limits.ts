// Shared text limits for faction forms and background generation.
// Keep aligned with backend/internal/faction/faction.go; the API enforces these too.
// Count Unicode code points after trimming surrounding whitespace, not UTF-16 code units.
export const maxDoctrineLength = 4000
export const maxBackgroundLength = 1000

// Shown wherever a Player who owns a faction would otherwise create one; keep the rule
// aligned with playerFactionLimit in backend/internal/faction/service/service.go.
export const factionLimitMessage = 'Players can own one faction at a time. Delete your faction to create a new one.'
