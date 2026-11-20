package service

import (
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
)

// Overrides are bounded independently of match length. Omission preserves the
// catalog default, including unpaced scenarios used by offline simulations.
func applyBattleTiming(scenario *game.Scenario, input CreateBattleInput) error {
	if input.TickDuration != nil {
		duration, err := time.ParseDuration(*input.TickDuration)
		if err != nil || duration < 100*time.Millisecond || duration > 10*time.Second {
			return ErrInvalidBattle
		}
		scenario.TickDuration = duration
	}
	if input.DecisionIntervalTicks != nil {
		interval := *input.DecisionIntervalTicks
		if interval < 1 || interval > 100 {
			return ErrInvalidBattle
		}
		scenario.DecisionIntervalTicks = controller.Tick(interval)
	}
	return nil
}
