// Shared text limits for faction forms and background generation.
// Keep aligned with backend/internal/faction/faction.go; the API enforces these too.
// Count Unicode code points after trimming surrounding whitespace, not UTF-16 code units.
export const maxDoctrineLength = 4000
export const maxBackgroundLength = 1000
