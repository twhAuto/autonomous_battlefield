// Checks the analytics summaries on small hand-checked battle sets: win records and
// intervals, decision rates, head-to-head records, units, upgrades and maps.

package analytics

import (
	"math"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

var (
	sonnet   = game.ControllerConfig{Type: "model", ProfileID: "claude", Provider: "anthropic", Runtime: "cli", ModelIdentifier: "sonnet", ReasoningEffort: "low"}
	haiku    = game.ControllerConfig{Type: "model", ProfileID: "claude", Provider: "anthropic", Runtime: "cli", ModelIdentifier: "haiku", ReasoningEffort: "low"}
	scripted = game.ControllerConfig{Type: "scripted"}
)

func side(faction string, controller game.ControllerConfig, stats *game.FactionStats) Side {
	return Side{FactionID: faction, FactionName: "Name " + faction, Controller: controller, Stats: stats}
}

func battle(scenario, winner, reason string, ticks uint64, home, away Side) Battle {
	b := Battle{ID: home.FactionID + away.FactionID, ScenarioID: scenario, Reason: reason, TicksRun: ticks, Sides: [2]Side{home, away}}
	if winner != "" {
		b.WinnerFactionID = &winner
	}
	return b
}

func near(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-4 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestWilsonInterval(t *testing.T) {
	if rate, low, high := Wilson(0, 0); rate != 0 || low != 0 || high != 0 {
		t.Fatalf("empty = %v %v %v", rate, low, high)
	}
	rate, low, high := Wilson(5, 10)
	near(t, "rate", rate, 0.5)
	near(t, "low", low, 0.2366)
	near(t, "high", high, 0.7634)
	_, low, high = Wilson(10, 10)
	near(t, "perfect low", low, 0.7225)
	near(t, "perfect high", high, 1)
}

func TestModelsFactionsAndHeadToHead(t *testing.T) {
	a := side("a", sonnet, nil)
	a.Decisions = Decisions{Model: 10, Fallbacks: 1, InvalidPlans: 2, Resolved: 8, LatencyMS: 8000, InputTokens: 3000, CachedInputTokens: 1000, OutputTokens: 300}
	battles := []Battle{
		battle("frontier", "a", "castle_destroyed", 100, a, side("s", scripted, nil)),
		battle("frontier", "", "tick_limit", 500, side("a", sonnet, nil), side("b", haiku, nil)),
		battle("castle", "b", "tick_limit", 200, side("b", haiku, nil), side("a", sonnet, nil)),
		battle("castle", "a", "castle_destroyed", 300, side("a", sonnet, nil), side("c", sonnet, nil)),
	}
	report := Aggregate(battles)
	if report.Battles != 4 || report.BattlesWithFallback != 1 || len(report.Models) != 3 {
		t.Fatalf("report = %+v", report)
	}
	// sonnet plays both sides of the mirror match: 5 appearances, 2 wins.
	best, middle, last := report.Models[0], report.Models[1], report.Models[2]
	if best.Controller.ModelIdentifier != "haiku" || best.Wins != 1 || best.Draws != 1 || best.Battles != 2 {
		t.Fatalf("haiku = %+v", best)
	}
	if middle.Controller.ModelIdentifier != "sonnet" || middle.Battles != 5 || middle.Wins != 2 || middle.Losses != 2 || middle.CastleWins != 2 || *middle.MeanTicksToWin != 200 {
		t.Fatalf("sonnet = %+v", middle)
	}
	near(t, "fallback rate", middle.FallbackRate, 0.1)
	near(t, "invalid plan rate", middle.InvalidPlanRate, 0.2)
	near(t, "latency", *middle.MeanDecisionLatencyMS, 1000)
	near(t, "input per battle", middle.TokensPerBattle.Input, 600)
	if last.Controller != (Controller{Type: "scripted"}) || last.Losses != 1 || last.MeanTicksToWin != nil || last.MeanDecisionLatencyMS != nil {
		t.Fatalf("scripted = %+v", last)
	}
	// a and b both win half; a played more battles, so it ranks first.
	if len(report.Factions) != 4 || report.Factions[0].FactionID != "a" || report.Factions[0].Battles != 4 || report.Factions[1].FactionName != "Name b" {
		t.Fatalf("factions = %+v", report.Factions)
	}
	requireHeadToHead(t, report.HeadToHead)
}

// requireHeadToHead checks that the mirror match is left out and each remaining pair
// appears both ways round.
func requireHeadToHead(t *testing.T, matchups []Matchup) {
	t.Helper()
	if len(matchups) != 4 {
		t.Fatalf("head to head = %+v", matchups)
	}
	for _, m := range matchups {
		if m.Controller.ModelIdentifier == "sonnet" && m.Opponent.ModelIdentifier == "haiku" && (m.Battles != 2 || m.Draws != 1 || m.Losses != 1) {
			t.Fatalf("sonnet v haiku = %+v", m)
		}
	}
}

func unitStats(recruited int, upgrades []game.UpgradePurchase, types ...game.UnitTypeStats) *game.FactionStats {
	return &game.FactionStats{UnitsRecruited: recruited, UnitTypes: types, Upgrades: upgrades}
}

func TestUnitsUpgradesAndMaps(t *testing.T) {
	knight := game.UnitTypeStats{UnitType: "knight", Started: 1, Recruited: 3, Lost: 2, Kills: 6, DamageDealt: 400, GoldSpent: 200}
	archer := game.UnitTypeStats{UnitType: "archer", Recruited: 1, Kills: 1, DamageDealt: 50}
	pikeman := game.UnitTypeStats{UnitType: "pikeman", Recruited: 4, Lost: 4, DamageDealt: 80, GoldSpent: 160}
	forge := []game.UpgradePurchase{{UpgradeID: "forge", Tick: 40}}
	battles := []Battle{
		battle("frontier", "a", "castle_destroyed", 100, side("a", sonnet, unitStats(4, forge, knight, archer)), side("b", haiku, unitStats(4, nil, pikeman))),
		battle("frontier", "b", "castle_destroyed", 300, side("a", sonnet, unitStats(3, []game.UpgradePurchase{{UpgradeID: "forge", Tick: 60}}, knight)), side("b", haiku, unitStats(4, forge, pikeman))),
		// Older results without per-type statistics count for outcomes only.
		battle("castle", "", "tick_limit", 500, side("a", sonnet, &game.FactionStats{}), side("b", haiku, nil)),
	}
	report := Aggregate(battles)
	if report.SidesWithUnitStats != 4 || len(report.Units) != 3 {
		t.Fatalf("units = %+v", report.Units)
	}
	units := map[string]UnitStats{}
	for _, u := range report.Units {
		units[u.UnitType] = u
	}
	k := units["knight"]
	if k.Fielded != 8 || k.Recruited != 6 || k.Kills != 12 || *k.KillsPerLoss != 3 || *k.DamagePerGold != 2 {
		t.Fatalf("knight = %+v", k)
	}
	if units["archer"].KillsPerLoss != nil || units["archer"].DamagePerGold != nil {
		t.Fatalf("archer = %+v", units["archer"])
	}
	// Winners recruited 3 knights + 1 archer, then 4 pikemen; losers 4 pikemen, then 3 knights.
	near(t, "knight share in wins", k.ShareInWins, 3.0/8)
	near(t, "knight share in losses", k.ShareInLosses, 3.0/7)
	near(t, "pikeman share in wins", units["pikeman"].ShareInWins, 4.0/8)
	if len(report.UnitsByModel) != 3 || report.UnitsByModel[2].UnitType != "knight" {
		t.Fatalf("units by model = %+v", report.UnitsByModel)
	}
	near(t, "sonnet knight share", report.UnitsByModel[2].ArmyShare, 6.0/7)
	if len(report.Upgrades) != 1 {
		t.Fatalf("upgrades = %+v", report.Upgrades)
	}
	forgeStats := report.Upgrades[0]
	near(t, "forge purchase rate", forgeStats.PurchaseRate, 0.75)
	near(t, "forge purchase tick", forgeStats.MeanPurchaseTick, 140.0/3)
	near(t, "forge win rate", *forgeStats.WinRateWhenPurchased, 2.0/3)
	near(t, "no forge win rate", *forgeStats.WinRateWhenNot, 0)
	if len(report.Maps) != 2 || report.Maps[0].ScenarioID != "castle" || report.Maps[0].DrawRate != 1 {
		t.Fatalf("maps = %+v", report.Maps)
	}
	frontier := report.Maps[1]
	if frontier.Battles != 2 || frontier.MeanTicks != 200 || frontier.Sides[0].Wins != 1 || frontier.Sides[1].WinRate != 0.5 {
		t.Fatalf("frontier = %+v", frontier)
	}
}

func TestRenamedFactionsKeepTheirNewestName(t *testing.T) {
	renamed := side("a", sonnet, nil)
	renamed.FactionName = "Amber Reborn"
	report := Aggregate([]Battle{
		battle("frontier", "a", "castle_destroyed", 90, renamed, side("b", haiku, nil)),
		battle("frontier", "b", "castle_destroyed", 90, side("a", sonnet, nil), side("b", haiku, nil)),
	})
	for _, f := range report.Factions {
		if f.FactionID == "a" && (f.FactionName != "Amber Reborn" || f.Battles != 2) {
			t.Fatalf("faction a = %+v", f)
		}
	}
}

func TestUpgradeBoughtByEverySideHasNoComparison(t *testing.T) {
	forge := []game.UpgradePurchase{{UpgradeID: "forge", Tick: 10}}
	unit := game.UnitTypeStats{UnitType: "knight", Recruited: 1}
	report := Aggregate([]Battle{battle("frontier", "a", "castle_destroyed", 50, side("a", sonnet, unitStats(1, forge, unit)), side("b", haiku, unitStats(1, forge, unit)))})
	if u := report.Upgrades[0]; u.WinRateWhenNot != nil || *u.WinRateWhenPurchased != 0.5 {
		t.Fatalf("forge = %+v", u)
	}
	if empty := Aggregate(nil); empty.Battles != 0 || len(empty.Models) != 0 || empty.Units == nil {
		t.Fatalf("empty report = %+v", empty)
	}
}
