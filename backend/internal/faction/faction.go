// Package faction defines the core faction model.
package faction

import "github.com/twh09/autonomous_battlefield/backend/internal/game"

type Faction struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	RulesetID   string         `json:"rulesetId"`
	Blueprint   game.Blueprint `json:"blueprint"`
	SpentPoints int64          `json:"spentPoints"`
	Remaining   int64          `json:"remainingPoints"`
}
