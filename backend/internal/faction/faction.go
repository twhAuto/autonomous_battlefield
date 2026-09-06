// Package faction defines the core faction model.
package faction

import "github.com/twh09/autonomous_battlefield/backend/internal/game"

const BalancedDoctrine = "Balance offense, defense, and economic growth. Protect your castle and income, scout before committing, and adapt purchases to visible threats."

type Faction struct {
	Doctrine  string         `json:"doctrine"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	RulesetID string         `json:"-"`
	Blueprint game.Blueprint `json:"-"`
}
