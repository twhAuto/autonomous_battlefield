// Territory and contested ground: a faction's territory is every hex within buildRange
// of its living, active buildings. Under a contested-territory ruleset no faction may
// build where its territory overlaps an enemy's.

package game

// claimsSite reports whether a living, active building puts site in its faction's territory.
func claimsSite(b Building, tick uint64, site Position) bool {
	return b.Health > 0 && b.ReadyAtTick <= tick && HexDistance(site, b.Position) <= effectiveBuildRange(b.BuildRange)
}

// contestedReason closes a site inside both the faction's territory and an enemy's. A site
// outside the faction's own territory is left to the build-range check.
func contestedReason(rules BuildingRules, state State, factionID string, site Position) string {
	if !rules.ContestedTerritory {
		return ""
	}
	own, enemy := false, false
	for _, b := range state.Buildings {
		if !claimsSite(b, state.Tick, site) {
			continue
		}
		if b.FactionID == factionID {
			own = true
		} else {
			enemy = true
		}
	}
	if own && enemy {
		return "contested_territory"
	}
	return ""
}

// placementReason adds the ruleset's dynamic placement rules, gold mines and contested
// territory, to ConstructionReason; legal sites and execution share it.
func (m *Match) placementReason(factionID, kind string, site Position) string {
	if reason := m.goldMineReason(kind, site); reason != "" {
		return reason
	}
	return contestedReason(m.scenario.Ruleset.Buildings, m.state, factionID, site)
}
