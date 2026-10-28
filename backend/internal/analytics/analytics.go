// Package analytics summarizes completed battles: which controllers and factions win,
// how reliable and costly model decisions are, which units and upgrades decide battles,
// and whether maps and spawn sides are fair. It is pure: storage and transport live
// with its callers.
package analytics

import (
	"math"
	"sort"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

// Battle is one completed battle; Sides are in spawn order.
type Battle struct {
	ID              string
	ScenarioID      string
	RulesVersion    string
	Reason          string
	WinnerFactionID *string
	TicksRun        uint64
	Sides           [2]Side
}

type Side struct {
	FactionID   string
	FactionName string
	Controller  game.ControllerConfig
	// Stats is nil when the result has no record for this faction.
	Stats     *game.FactionStats
	Decisions Decisions
}

// Decisions totals one side's model decisions in the battle's final attempt.
type Decisions struct {
	Model             int
	Fallbacks         int
	InvalidPlans      int
	Resolved          int
	LatencyMS         float64
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
}

// HasFallback reports whether a model decision fell back to the scripted plan.
func (b Battle) HasFallback() bool {
	return b.Sides[0].Decisions.Fallbacks > 0 || b.Sides[1].Decisions.Fallbacks > 0
}

// result is a side's outcome: 1 win, 0 draw, -1 loss.
func (b Battle) result(side int) int {
	switch {
	case b.WinnerFactionID == nil:
		return 0
	case *b.WinnerFactionID == b.Sides[side].FactionID:
		return 1
	default:
		return -1
	}
}

// Controller identifies who played a side. Scripted controllers carry only their type.
type Controller struct {
	Type            string `json:"type"`
	ProfileID       string `json:"profileId,omitempty"`
	Provider        string `json:"provider,omitempty"`
	Runtime         string `json:"runtime,omitempty"`
	ModelIdentifier string `json:"modelIdentifier,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

func controllerOf(c game.ControllerConfig) Controller {
	if c.Type != "model" {
		return Controller{Type: c.Type}
	}
	return Controller{Type: c.Type, ProfileID: c.ProfileID, Provider: c.Provider, Runtime: c.Runtime, ModelIdentifier: c.ModelIdentifier, ReasoningEffort: c.ReasoningEffort}
}

func (c Controller) less(other Controller) bool {
	a := [...]string{c.Type, c.ProfileID, c.ModelIdentifier, c.ReasoningEffort, c.Provider, c.Runtime}
	b := [...]string{other.Type, other.ProfileID, other.ModelIdentifier, other.ReasoningEffort, other.Provider, other.Runtime}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// Outcome is a win record. WinRate counts draws as non-wins; the interval is the 95%
// Wilson score interval, so small samples show their uncertainty.
type Outcome struct {
	Battles        int      `json:"battles"`
	Wins           int      `json:"wins"`
	Draws          int      `json:"draws"`
	Losses         int      `json:"losses"`
	CastleWins     int      `json:"castleWins"`
	WinRate        float64  `json:"winRate"`
	WinRateLow     float64  `json:"winRateLow"`
	WinRateHigh    float64  `json:"winRateHigh"`
	MeanTicksToWin *float64 `json:"meanTicksToWin"`
	winTicks       uint64
}

func (o *Outcome) add(b Battle, side int) {
	o.Battles++
	switch b.result(side) {
	case 1:
		o.Wins++
		o.winTicks += b.TicksRun
		if b.Reason == "castle_destroyed" {
			o.CastleWins++
		}
	case 0:
		o.Draws++
	default:
		o.Losses++
	}
}

func (o *Outcome) finish() {
	o.WinRate, o.WinRateLow, o.WinRateHigh = Wilson(o.Wins, o.Battles)
	if o.Wins > 0 {
		mean := float64(o.winTicks) / float64(o.Wins)
		o.MeanTicksToWin = &mean
	}
}

// Wilson returns the share wins/n with its 95% Wilson score interval; zero for n == 0.
func Wilson(wins, n int) (rate, low, high float64) {
	if n == 0 {
		return 0, 0, 0
	}
	const z = 1.959964
	p, count := float64(wins)/float64(n), float64(n)
	denominator := 1 + z*z/count
	center := (p + z*z/(2*count)) / denominator
	margin := z * math.Sqrt(p*(1-p)/count+z*z/(4*count*count)) / denominator
	return p, math.Max(0, center-margin), math.Min(1, center+margin)
}

type Tokens struct {
	Input       float64 `json:"input"`
	CachedInput float64 `json:"cachedInput"`
	Output      float64 `json:"output"`
}

type ModelStats struct {
	Controller Controller `json:"controller"`
	Outcome
	Decisions             int      `json:"decisions"`
	FallbackRate          float64  `json:"fallbackRate"`
	InvalidPlanRate       float64  `json:"invalidPlanRate"`
	MeanDecisionLatencyMS *float64 `json:"meanDecisionLatencyMs"`
	TokensPerBattle       Tokens   `json:"tokensPerBattle"`
	totals                Decisions
}

type FactionStats struct {
	FactionID   string `json:"factionId"`
	FactionName string `json:"factionName"`
	Outcome
}

// Matchup is Controller's record against Opponent; each pair appears both ways round.
type Matchup struct {
	Controller Controller `json:"controller"`
	Opponent   Controller `json:"opponent"`
	Battles    int        `json:"battles"`
	Wins       int        `json:"wins"`
	Draws      int        `json:"draws"`
	Losses     int        `json:"losses"`
}

// Report is the summary of a set of completed battles. The service fills the filter
// and truncation fields; a nil RulesVersion means every rules version.
type Report struct {
	RulesVersion        *string        `json:"rulesVersion"`
	Truncated           bool           `json:"truncated"`
	ExcludedForFallback int            `json:"excludedForFallback"`
	Battles             int            `json:"battles"`
	BattlesWithFallback int            `json:"battlesWithFallback"`
	SidesWithUnitStats  int            `json:"sidesWithUnitStats"`
	Models              []ModelStats   `json:"models"`
	Factions            []FactionStats `json:"factions"`
	HeadToHead          []Matchup      `json:"headToHead"`
	Units               []UnitStats    `json:"units"`
	UnitsByModel        []ModelUnit    `json:"unitsByModel"`
	Upgrades            []UpgradeStats `json:"upgrades"`
	Maps                []MapStats     `json:"maps"`
}

// Aggregate summarizes battles. Every list is sorted, so equal input gives equal output.
func Aggregate(battles []Battle) Report {
	report := Report{Battles: len(battles)}
	for _, b := range battles {
		if b.HasFallback() {
			report.BattlesWithFallback++
		}
	}
	report.Models = models(battles)
	report.Factions = factions(battles)
	report.HeadToHead = headToHead(battles)
	units := newUnitTally()
	for _, b := range battles {
		units.add(b)
	}
	report.SidesWithUnitStats = units.sides
	report.Units, report.UnitsByModel, report.Upgrades = units.units(), units.byModel(), units.upgrades()
	report.Maps = maps(battles)
	return report
}

func models(battles []Battle) []ModelStats {
	byController := map[Controller]*ModelStats{}
	for _, b := range battles {
		for side := range b.Sides {
			key := controllerOf(b.Sides[side].Controller)
			if byController[key] == nil {
				byController[key] = &ModelStats{Controller: key}
			}
			stats := byController[key]
			stats.add(b, side)
			stats.totals = addDecisions(stats.totals, b.Sides[side].Decisions)
		}
	}
	result := make([]ModelStats, 0, len(byController))
	for _, stats := range byController {
		stats.finish()
		stats.finishDecisions()
		result = append(result, *stats)
	}
	sort.Slice(result, func(i, j int) bool {
		return outcomeBefore(result[i].Outcome, result[j].Outcome, result[i].Controller.less(result[j].Controller))
	})
	return result
}

func addDecisions(total, d Decisions) Decisions {
	total.Model += d.Model
	total.Fallbacks += d.Fallbacks
	total.InvalidPlans += d.InvalidPlans
	total.Resolved += d.Resolved
	total.LatencyMS += d.LatencyMS
	total.InputTokens += d.InputTokens
	total.CachedInputTokens += d.CachedInputTokens
	total.OutputTokens += d.OutputTokens
	return total
}

func (m *ModelStats) finishDecisions() {
	t := m.totals
	m.Decisions = t.Model
	if t.Model > 0 {
		m.FallbackRate = float64(t.Fallbacks) / float64(t.Model)
		m.InvalidPlanRate = float64(t.InvalidPlans) / float64(t.Model)
	}
	if t.Resolved > 0 {
		mean := t.LatencyMS / float64(t.Resolved)
		m.MeanDecisionLatencyMS = &mean
	}
	battles := float64(m.Battles)
	m.TokensPerBattle = Tokens{Input: float64(t.InputTokens) / battles, CachedInput: float64(t.CachedInputTokens) / battles, Output: float64(t.OutputTokens) / battles}
}

// outcomeBefore ranks by win rate, then more battles, then the tiebreak.
func outcomeBefore(a, b Outcome, tiebreak bool) bool {
	if a.WinRate != b.WinRate {
		return a.WinRate > b.WinRate
	}
	if a.Battles != b.Battles {
		return a.Battles > b.Battles
	}
	return tiebreak
}

func factions(battles []Battle) []FactionStats {
	byID := map[string]*FactionStats{}
	for _, b := range battles {
		for side, s := range b.Sides {
			if byID[s.FactionID] == nil {
				byID[s.FactionID] = &FactionStats{FactionID: s.FactionID}
			}
			byID[s.FactionID].FactionName = s.FactionName
			byID[s.FactionID].add(b, side)
		}
	}
	result := make([]FactionStats, 0, len(byID))
	for _, stats := range byID {
		stats.finish()
		result = append(result, *stats)
	}
	sort.Slice(result, func(i, j int) bool {
		return outcomeBefore(result[i].Outcome, result[j].Outcome, result[i].FactionID < result[j].FactionID)
	})
	return result
}

func headToHead(battles []Battle) []Matchup {
	pairs := map[[2]Controller]*Matchup{}
	for _, b := range battles {
		first, second := controllerOf(b.Sides[0].Controller), controllerOf(b.Sides[1].Controller)
		if first == second {
			continue
		}
		for side, pair := range [2][2]Controller{{first, second}, {second, first}} {
			if pairs[pair] == nil {
				pairs[pair] = &Matchup{Controller: pair[0], Opponent: pair[1]}
			}
			m := pairs[pair]
			m.Battles++
			switch b.result(side) {
			case 1:
				m.Wins++
			case 0:
				m.Draws++
			default:
				m.Losses++
			}
		}
	}
	result := make([]Matchup, 0, len(pairs))
	for _, m := range pairs {
		result = append(result, *m)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Controller != result[j].Controller {
			return result[i].Controller.less(result[j].Controller)
		}
		return result[i].Opponent.less(result[j].Opponent)
	})
	return result
}
