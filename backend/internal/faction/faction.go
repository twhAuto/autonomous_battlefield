// Package faction defines the faction profile model and shared text limits.
// Profile validation lives in the service subpackage; persistence lives in repo.
package faction

import "github.com/twh09/autonomous_battlefield/backend/internal/game"

// Profile limits count Unicode code points after trimming surrounding whitespace.
// Keep these values aligned with frontend/src/features/factions/limits.ts.
const (
	MaxDoctrineLength   = 4000
	MaxBackgroundLength = 1000
)

// BalancedDoctrine is the baseline strategy also offered by the frontend's Balanced template.
const BalancedDoctrine = "Balance offense, defense, and economic growth. Protect your castle and income, scout before committing, and adapt purchases to visible threats."

// Faction contains the public profile and internal game configuration.
// RulesetID and Blueprint are excluded from JSON API responses.
type Faction struct {
	Background string         `json:"background,omitempty"`
	Doctrine   string         `json:"doctrine"`
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	RulesetID  string         `json:"-"`
	Blueprint  game.Blueprint `json:"-"`
}
