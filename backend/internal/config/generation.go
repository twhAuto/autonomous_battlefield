// Parses and validates generation configuration.
package config

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/generation"
)

func parseBackgroundGeneration(raw fileConfig) (generation.Config, error) {
	c := generation.DefaultConfig()
	v := raw.AI.BackgroundGeneration
	if v.Enabled != nil {
		c.Enabled = *v.Enabled
	}
	if v.ProfileID != "" {
		c.ProfileID = v.ProfileID
	}
	if v.DailyAllowance != nil {
		c.DailyAllowance = *v.DailyAllowance
	}
	var err error
	c.Cooldown, err = parseOptionalDuration("ai.backgroundGeneration.cooldown", v.Cooldown, c.Cooldown, false)
	if err != nil {
		return c, err
	}
	c.QueueTimeout, err = parseOptionalDuration("ai.backgroundGeneration.queueTimeout", v.QueueTimeout, c.QueueTimeout, false)
	if err != nil {
		return c, err
	}
	c.ExecutionTimeout, err = parseOptionalDuration("ai.backgroundGeneration.executionTimeout", v.ExecutionTimeout, c.ExecutionTimeout, false)
	if err != nil {
		return c, err
	}
	return c, c.Validate()
}

// Reject repeated JSON role keys rather than silently accepting the last assignment.
