// Package game contains the deterministic, infrastructure-free castle-siege
// simulation domain.
package game

import (
	"errors"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
)

const (
	ActionAttack = "attack"
	ActionDefend = "defend"

	TerrainGround    = "ground"
	TerrainRock      = "rock"
	TerrainBuildSite = "build_site"

	BuildingCastle  = "castle"
	BuildingEconomy = "economy"
)

var (
	ErrInvalidRuleset   = errors.New("invalid faction ruleset")
	ErrInvalidBlueprint = errors.New("invalid faction blueprint")
	ErrInvalidScenario  = errors.New("invalid castle-siege scenario")
	ErrMatchFinished    = errors.New("match already finished")
)

type Position struct {
	X int `json:"x" yaml:"x"`
	Y int `json:"y" yaml:"y"`
}

type Map struct {
	Kind         string        `json:"kind" yaml:"kind"`
	Width        int           `json:"width" yaml:"width"`
	Height       int           `json:"height" yaml:"height"`
	Rows         []string      `json:"rows" yaml:"rows"`
	CastleSpawns [2]Position   `json:"castleSpawns" yaml:"castleSpawns"`
	EconomySites [2][]Position `json:"economySites" yaml:"economySites"`
}

type UnitType struct {
	ID       string `json:"id" yaml:"id"`
	Cost     int64  `json:"cost" yaml:"cost"`
	Health   int64  `json:"health" yaml:"health"`
	Attack   int64  `json:"attack" yaml:"attack"`
	Defense  int64  `json:"defense" yaml:"defense"`
	Speed    int64  `json:"speed" yaml:"speed"`
	Movement int    `json:"movement" yaml:"movement"`
	Range    int    `json:"range" yaml:"range"`
	Vision   int    `json:"vision" yaml:"vision"`
}

type Upgrade struct {
	ID                 string `json:"id" yaml:"id"`
	Cost               int64  `json:"cost" yaml:"cost"`
	AttackBonus        int64  `json:"attackBonus" yaml:"attackBonus"`
	DefenseBonus       int64  `json:"defenseBonus" yaml:"defenseBonus"`
	EconomyIncomeBonus int64  `json:"economyIncomeBonus" yaml:"economyIncomeBonus"`
}

type BuildingRules struct {
	CastleHealth        int64 `json:"castleHealth" yaml:"castleHealth"`
	CastleDefense       int64 `json:"castleDefense" yaml:"castleDefense"`
	CastleVision        int   `json:"castleVision" yaml:"castleVision"`
	CastleIncome        int64 `json:"castleIncome" yaml:"castleIncome"`
	EconomyCost         int64 `json:"economyCost" yaml:"economyCost"`
	EconomyHealth       int64 `json:"economyHealth" yaml:"economyHealth"`
	EconomyDefense      int64 `json:"economyDefense" yaml:"economyDefense"`
	EconomyVision       int   `json:"economyVision" yaml:"economyVision"`
	EconomyIncome       int64 `json:"economyIncome" yaml:"economyIncome"`
	MaximumEconomyCount int   `json:"maximumEconomyCount" yaml:"maximumEconomyCount"`
}

type CombatRules struct {
	MinimumDamage            int64 `json:"minimumDamage" yaml:"minimumDamage"`
	DefendAttackPercent      int64 `json:"defendAttackPercent" yaml:"defendAttackPercent"`
	DefendDamageTakenPercent int64 `json:"defendDamageTakenPercent" yaml:"defendDamageTakenPercent"`
	LootPercent              int64 `json:"lootPercent" yaml:"lootPercent"`
}

type Ruleset struct {
	ID           string        `json:"id" yaml:"id"`
	Version      string        `json:"version" yaml:"version"`
	DesignBudget int64         `json:"designBudget" yaml:"designBudget"`
	StartingGold int64         `json:"startingGold" yaml:"startingGold"`
	Units        []UnitType    `json:"units" yaml:"units"`
	Upgrades     []Upgrade     `json:"upgrades" yaml:"upgrades"`
	Buildings    BuildingRules `json:"buildings" yaml:"buildings"`
	Combat       CombatRules   `json:"combat" yaml:"combat"`
}

type UnitSelection struct {
	Type  string `json:"type" yaml:"type"`
	Count int    `json:"count" yaml:"count"`
}

type Blueprint struct {
	Units               []UnitSelection `json:"units" yaml:"units"`
	UpgradeIDs          []string        `json:"upgradeIds" yaml:"upgradeIds"`
	RecruitmentPriority []string        `json:"recruitmentPriority" yaml:"recruitmentPriority"`
}

type FactionSetup struct {
	ID            string                    `json:"id" yaml:"id"`
	Name          string                    `json:"name" yaml:"name"`
	Blueprint     Blueprint                 `json:"blueprint" yaml:"blueprint"`
	FallbackPlan  controller.PlanProposal   `json:"fallbackPlan" yaml:"fallbackPlan"`
	ScriptedPlans []controller.PlanProposal `json:"scriptedPlans" yaml:"scriptedPlans"`
	Controller    ControllerConfig          `json:"controller" yaml:"controller"`
}

type ControllerConfig struct {
	Type            string `json:"type" yaml:"type"`
	ProfileID       string `json:"profileId,omitempty" yaml:"profileId,omitempty"`
	Provider        string `json:"provider,omitempty" yaml:"provider,omitempty"`
	Runtime         string `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	ModelIdentifier string `json:"modelIdentifier,omitempty" yaml:"modelIdentifier,omitempty"`
}

type Scenario struct {
	ID                    string          `json:"id"`
	RulesVersion          string          `json:"rulesVersion"`
	RulesetID             string          `json:"rulesetId"`
	MaximumTicks          uint64          `json:"maximumTicks"`
	TickDuration          time.Duration   `json:"tickDuration"`
	DecisionIntervalTicks controller.Tick `json:"decisionIntervalTicks"`
	DecisionLeadTicks     controller.Tick `json:"-"` // Deprecated compatibility field; production decisions have no lead phase.
	Map                   Map             `json:"map"`
	Ruleset               Ruleset         `json:"ruleset"`
	Factions              []FactionSetup  `json:"factions"`
}

type Unit struct {
	ID                   string    `json:"id"`
	FactionID            string    `json:"factionId"`
	Type                 string    `json:"type"`
	Position             Position  `json:"position"`
	Health               int64     `json:"health"`
	MaxHealth            int64     `json:"maxHealth"`
	Attack               int64     `json:"attack"`
	Defense              int64     `json:"defense"`
	Speed                int64     `json:"speed"`
	Movement             int       `json:"movement"`
	Range                int       `json:"range"`
	Vision               int       `json:"vision"`
	Cost                 int64     `json:"cost"`
	ReadyAtTick          uint64    `json:"readyAtTick"`
	MovedFrom            *Position `json:"movedFrom,omitempty"`
	AttackTargetID       string    `json:"attackTargetId,omitempty"`
	AttackTargetPosition *Position `json:"attackTargetPosition,omitempty"`
}

type Building struct {
	ID        string   `json:"id"`
	FactionID string   `json:"factionId"`
	Type      string   `json:"type"`
	Position  Position `json:"position"`
	Health    int64    `json:"health"`
	MaxHealth int64    `json:"maxHealth"`
	Defense   int64    `json:"defense"`
	Vision    int      `json:"vision"`
	Cost      int64    `json:"cost"`
	Income    int64    `json:"income"`
}

type FactionState struct {
	FactionID          string `json:"factionId"`
	Gold               int64  `json:"gold"`
	RecruitmentCursor  int    `json:"recruitmentCursor"`
	GoldEarned         int64  `json:"goldEarned"`
	GoldSpent          int64  `json:"goldSpent"`
	GoldLooted         int64  `json:"goldLooted"`
	UnitsStarted       int    `json:"unitsStarted"`
	UnitsRecruited     int    `json:"unitsRecruited"`
	UnitsLost          int    `json:"unitsLost"`
	BuildingsBuilt     int    `json:"buildingsBuilt"`
	BuildingsDestroyed int    `json:"buildingsDestroyed"`
}

type State struct {
	BattleID  string         `json:"battleId"`
	Tick      uint64         `json:"tick"`
	Factions  []FactionState `json:"factions"`
	Units     []Unit         `json:"units"`
	Buildings []Building     `json:"buildings"`
}

type Event struct {
	Sequence uint64         `json:"sequence"`
	Tick     uint64         `json:"tick"`
	Type     string         `json:"type"`
	Data     map[string]any `json:"data"`
}

type FactionStats struct {
	FactionID            string `json:"factionId"`
	CastleHealth         int64  `json:"castleHealth"`
	UnitsStarted         int    `json:"unitsStarted"`
	UnitsRecruited       int    `json:"unitsRecruited"`
	UnitsLost            int    `json:"unitsLost"`
	UnitsRemaining       int    `json:"unitsRemaining"`
	DamageDealt          int64  `json:"damageDealt"`
	DamageTaken          int64  `json:"damageTaken"`
	AttackTicks          uint64 `json:"attackTicks"`
	DefendTicks          uint64 `json:"defendTicks"`
	GoldEarned           int64  `json:"goldEarned"`
	GoldSpent            int64  `json:"goldSpent"`
	GoldLooted           int64  `json:"goldLooted"`
	BuildingsConstructed int    `json:"buildingsConstructed"`
	BuildingsDestroyed   int    `json:"buildingsDestroyed"`
}

type Result struct {
	WinnerFactionID *string        `json:"winnerFactionId"`
	Reason          string         `json:"reason"`
	TicksRun        uint64         `json:"ticksRun"`
	Factions        []FactionStats `json:"factions"`
}

type Transition struct {
	Tick     uint64  `json:"tick"`
	Events   []Event `json:"events"`
	State    State   `json:"state"`
	Finished bool    `json:"finished"`
	Result   *Result `json:"result,omitempty"`
}

// DecisionSnapshot is the bounded, faction-visible state frozen immediately
// before a production window. It contains no mutation capability.
type DecisionSnapshot struct {
	BattleID        string                  `json:"battleId"`
	FactionID       string                  `json:"factionId"`
	TargetTick      uint64                  `json:"targetTick"`
	Version         uint64                  `json:"version"`
	View            View                    `json:"view"`
	Gold            int64                   `json:"gold"`
	ProjectedIncome int64                   `json:"projectedIncome"`
	EconomyCost     int64                   `json:"economyCost"`
	EconomyIncome   int64                   `json:"economyIncome"`
	EconomyCount    int                     `json:"economyCount"`
	MaximumEconomy  int                     `json:"maximumEconomy"`
	LegalUnits      []UnitType              `json:"legalUnits"`
	LegalSites      []controller.Coordinate `json:"legalSites"`
	ActivePlan      controller.PlanProposal `json:"activePlan"`
}

type View struct {
	Map              Map        `json:"map"`
	State            State      `json:"state"`
	VisiblePositions []Position `json:"visiblePositions"`
}
