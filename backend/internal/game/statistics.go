// Counts each faction's activity per unit type and building type, and when it bought
// each upgrade, so a battle result shows which units and upgrades decided it.

package game

import "sort"

// typeStatistics belongs to a faction runtime. Entries appear on first activity, so a
// type the faction never fielded or faced is absent rather than zero.
type typeStatistics struct {
	units     map[string]*UnitTypeStats
	buildings map[string]*BuildingTypeStats
	upgrades  []UpgradePurchase
}

func (s *typeStatistics) unit(kind string) *UnitTypeStats {
	if s.units == nil {
		s.units = make(map[string]*UnitTypeStats)
	}
	if s.units[kind] == nil {
		s.units[kind] = &UnitTypeStats{UnitType: kind}
	}
	return s.units[kind]
}

func (s *typeStatistics) building(kind string) *BuildingTypeStats {
	if s.buildings == nil {
		s.buildings = make(map[string]*BuildingTypeStats)
	}
	if s.buildings[kind] == nil {
		s.buildings[kind] = &BuildingTypeStats{BuildingType: kind}
	}
	return s.buildings[kind]
}

func (s *typeStatistics) unitList() []UnitTypeStats {
	values := make([]UnitTypeStats, 0, len(s.units))
	for _, value := range s.units {
		values = append(values, *value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].UnitType < values[j].UnitType })
	return values
}

func (s *typeStatistics) buildingList() []BuildingTypeStats {
	values := make([]BuildingTypeStats, 0, len(s.buildings))
	for _, value := range s.buildings {
		values = append(values, *value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].BuildingType < values[j].BuildingType })
	return values
}

func (s *typeStatistics) upgradeList() []UpgradePurchase {
	return append([]UpgradePurchase(nil), s.upgrades...)
}

func restoreTypeStatistics(units []UnitTypeStats, buildings []BuildingTypeStats, upgrades []UpgradePurchase) typeStatistics {
	var stats typeStatistics
	for _, value := range units {
		*stats.unit(value.UnitType) = value
	}
	for _, value := range buildings {
		*stats.building(value.BuildingType) = value
	}
	stats.upgrades = append([]UpgradePurchase(nil), upgrades...)
	return stats
}

// recordStrike credits one landed attack to the attacker's unit or building type and
// charges it to the target's type. source is the attacking building, or nil for a unit.
func (m *Match) recordStrike(attacker *Unit, source *Building, target *attackTarget, damage int64) {
	destroyed := target.health() <= 0
	own := &m.factions[attacker.FactionID].stats
	if source != nil {
		credit := own.building(source.Type)
		credit.DamageDealt += damage
		if destroyed && target.unit != nil {
			credit.Kills++
		}
	} else {
		credit := own.unit(attacker.Type)
		credit.DamageDealt += damage
		if destroyed && target.unit != nil {
			credit.Kills++
		} else if destroyed {
			credit.BuildingsDestroyed++
		}
	}
	enemy := &m.factions[target.factionID].stats
	if target.unit != nil {
		charge := enemy.unit(target.unit.Type)
		charge.DamageTaken += damage
		if destroyed {
			charge.Lost++
		}
		return
	}
	charge := enemy.building(target.kind)
	charge.DamageTaken += damage
	if destroyed {
		charge.Lost++
	}
}

// killerData names what landed the destroying blow, for destruction events.
func killerData(attacker *Unit, source *Building) map[string]any {
	kind := attacker.Type
	if source != nil {
		kind = source.Type
	}
	return map[string]any{"killerId": attacker.ID, "killerType": kind, "killerFactionId": attacker.FactionID}
}
