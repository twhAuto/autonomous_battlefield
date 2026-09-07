// Package faction defines the core faction model.
package faction

import "github.com/twh09/autonomous_battlefield/backend/internal/game"

// Profile limits count Unicode code points after whitespace normalization.
const (
	MaxDoctrineLength   = 4000
	MaxBackgroundLength = 1000
)

const BalancedDoctrine = "Balance offense, defense, and economic growth. Protect your castle and income, scout before committing, and adapt purchases to visible threats."

type Faction struct {
	Background string         `json:"background,omitempty"`
	Doctrine   string         `json:"doctrine"`
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	RulesetID  string         `json:"-"`
	Blueprint  game.Blueprint `json:"-"`
}
