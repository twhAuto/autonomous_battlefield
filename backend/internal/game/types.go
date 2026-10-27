// Package game contains the deterministic, infrastructure-free castle-siege
// simulation domain.
package game

import (
	"errors"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

const (
	ActionAttack = "attack"
	ActionDefend = "defend"

	TerrainForest    = "forest"
	TerrainGold      = "gold_deposit"
	TerrainGround    = "ground"
	TerrainRock      = "rock"
	TerrainBuildSite = "build_site" // Deprecated authored symbol, retained for saved maps.

	BuildingCastle        = "castle"
	BuildingEconomy       = "economy"
	BuildingTower         = "defense_tower"
	BuildingWatchtower    = "watchtower" // Legacy snapshots only.
	BuildingOutpost       = "outpost"
	BuildingBarracks      = "barracks"
	BuildingArcheryRange  = "archery_range"
	BuildingStables       = "stables"
	BuildingSiegeWorkshop = "siege_workshop"
	BuildingInfirmary     = "infirmary"
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

// Vision fields remain decodable for historical content; the engine does not use them.
type UnitType struct {
	TrainingTicks        uint64 `json:"trainingTicks,omitempty" yaml:"trainingTicks,omitempty"`
	Scout                bool   `json:"scout,omitempty" yaml:"scout,omitempty"`
	PreferredTargetClass string `json:"preferredTargetClass,omitempty" yaml:"preferredTargetClass,omitempty"`
	ArmorPiercing        int64  `json:"armorPiercing,omitempty" yaml:"armorPiercing,omitempty"`
	RepairAmount         int64  `json:"repairAmount,omitempty" yaml:"repairAmount,omitempty"`
	RepairCost           int64  `json:"repairCost,omitempty" yaml:"repairCost,omitempty"`
	RepairCharges        int    `json:"repairCharges,omitempty" yaml:"repairCharges,omitempty"`
	HealRange            int    `json:"healRange,omitempty" yaml:"healRange,omitempty"`

	AttackMode          string `json:"attackMode,omitempty" yaml:"attackMode,omitempty"`
	Class               string `json:"class,omitempty" yaml:"class,omitempty"`
	MinimumRange        int    `json:"minimumRange,omitempty" yaml:"minimumRange,omitempty"`
	BuildingPriority    bool   `json:"buildingPriority,omitempty" yaml:"buildingPriority,omitempty"`
	BuildingDamageBonus int64  `json:"buildingDamageBonus,omitempty" yaml:"buildingDamageBonus,omitempty"`
	BonusAgainstClass   string `json:"bonusAgainstClass,omitempty" yaml:"bonusAgainstClass,omitempty"`
	ClassDamageBonus    int64  `json:"classDamageBonus,omitempty" yaml:"classDamageBonus,omitempty"`
	HealAmount          int64  `json:"healAmount,omitempty" yaml:"healAmount,omitempty"`
	// RequiresUpgradeID locks training until the faction owns that upgrade.
	RequiresUpgradeID string `json:"requiresUpgradeId,omitempty" yaml:"requiresUpgradeId,omitempty"`

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
	TargetUnit          string `json:"targetUnit,omitempty" yaml:"targetUnit,omitempty"`
	TargetClass         string `json:"targetClass,omitempty" yaml:"targetClass,omitempty"`
	ExclusiveGroup      string `json:"exclusiveGroup,omitempty" yaml:"exclusiveGroup,omitempty"`
	MovementBonus       int    `json:"movementBonus,omitempty" yaml:"movementBonus,omitempty"`
	RangeBonus          int    `json:"rangeBonus,omitempty" yaml:"rangeBonus,omitempty"`
	HealAmountBonus     int64  `json:"healAmountBonus,omitempty" yaml:"healAmountBonus,omitempty"`
	HealRangeBonus      int    `json:"healRangeBonus,omitempty" yaml:"healRangeBonus,omitempty"`
	BuildingDamageBonus int64  `json:"buildingDamageBonus,omitempty" yaml:"buildingDamageBonus,omitempty"`

	Track              string `json:"track,omitempty" yaml:"track,omitempty"`
	Level              int    `json:"level,omitempty" yaml:"level,omitempty"`
	RequiresUpgradeID  string `json:"requiresUpgradeId,omitempty" yaml:"requiresUpgradeId,omitempty"`
	ID                 string `json:"id" yaml:"id"`
	Cost               int64  `json:"cost" yaml:"cost"`
	AttackBonus        int64  `json:"attackBonus" yaml:"attackBonus"`
	DefenseBonus       int64  `json:"defenseBonus" yaml:"defenseBonus"`
	EconomyIncomeBonus int64  `json:"economyIncomeBonus" yaml:"economyIncomeBonus"`
}

type BuildingRules struct {
	CastleTrains         []string `json:"castleTrains,omitempty" yaml:"castleTrains,omitempty"`
	CastleAttack         int64    `json:"castleAttack,omitempty" yaml:"castleAttack,omitempty"`
	CastleRange          int      `json:"castleRange,omitempty" yaml:"castleRange,omitempty"`
	CastleBuildRange     int      `json:"castleBuildRange,omitempty" yaml:"castleBuildRange,omitempty"`
	EconomyBuildRange    int      `json:"economyBuildRange,omitempty" yaml:"economyBuildRange,omitempty"`
	MaximumBuildingCount int      `json:"maximumBuildingCount,omitempty" yaml:"maximumBuildingCount,omitempty"`
	CastleTypeID         string   `json:"castleTypeId,omitempty" yaml:"castleTypeId,omitempty"`
	EconomyTypeID        string   `json:"economyTypeId,omitempty" yaml:"economyTypeId,omitempty"`
	CastleHealth         int64    `json:"castleHealth" yaml:"castleHealth"`
	CastleDefense        int64    `json:"castleDefense" yaml:"castleDefense"`
	CastleVision         int      `json:"castleVision" yaml:"castleVision"`
	CastleIncome         int64    `json:"castleIncome" yaml:"castleIncome"`
	EconomyCost          int64    `json:"economyCost" yaml:"economyCost"`
	EconomyHealth        int64    `json:"economyHealth" yaml:"economyHealth"`
	EconomyDefense       int64    `json:"economyDefense" yaml:"economyDefense"`
	EconomyVision        int      `json:"economyVision" yaml:"economyVision"`
	EconomyIncome        int64    `json:"economyIncome" yaml:"economyIncome"`
	MaximumEconomyCount  int      `json:"maximumEconomyCount" yaml:"maximumEconomyCount"`
}

type CombatRules struct {
	MinimumDamage            int64 `json:"minimumDamage" yaml:"minimumDamage"`
	DefendAttackPercent      int64 `json:"defendAttackPercent" yaml:"defendAttackPercent"`
	DefendDamageTakenPercent int64 `json:"defendDamageTakenPercent" yaml:"defendDamageTakenPercent"`
	LootPercent              int64 `json:"lootPercent" yaml:"lootPercent"`
}

type UnitCard struct {
	UnitID     string `json:"unitId" yaml:"unitId"`
	Role       string `json:"role" yaml:"role"`
	Strengths  string `json:"strengths" yaml:"strengths"`
	Weaknesses string `json:"weaknesses" yaml:"weaknesses"`
	Guidance   string `json:"guidance" yaml:"guidance"`
}
type RulesKnowledge struct {
	Mechanics string     `json:"mechanics" yaml:"mechanics"`
	Cards     []UnitCard `json:"cards" yaml:"cards"`
}

type ContentSource struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

type Ruleset struct {
	ContinuousProduction bool            `json:"continuousProduction,omitempty" yaml:"continuousProduction,omitempty"`
	Structures           []BuildingType  `json:"structures,omitempty" yaml:"structures,omitempty"`
	ContentSource        *ContentSource  `json:"contentSource,omitempty" yaml:"contentSource,omitempty"`
	Knowledge            *RulesKnowledge `json:"knowledge,omitempty" yaml:"knowledge,omitempty"`
	ID                   string          `json:"id" yaml:"id"`
	Version              string          `json:"version" yaml:"version"`
	DesignBudget         int64           `json:"designBudget,omitempty" yaml:"designBudget"`
	StartingGold         int64           `json:"startingGold" yaml:"startingGold"`
	// MaximumUnitCount caps living plus paid in-training units per faction; zero is unlimited.
	MaximumUnitCount int           `json:"maximumUnitCount,omitempty" yaml:"maximumUnitCount,omitempty"`
	Units            []UnitType    `json:"units" yaml:"units"`
	Upgrades         []Upgrade     `json:"upgrades" yaml:"upgrades"`
	Buildings        BuildingRules `json:"buildings" yaml:"buildings"`
	Combat           CombatRules   `json:"combat" yaml:"combat"`
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
	Background    string                    `json:"background,omitempty"`
	Doctrine      string                    `json:"doctrine,omitempty"`
	ID            string                    `json:"id" yaml:"id"`
	Name          string                    `json:"name" yaml:"name"`
	Blueprint     Blueprint                 `json:"blueprint" yaml:"blueprint"`
	FallbackPlan  controller.PlanProposal   `json:"fallbackPlan" yaml:"fallbackPlan"`
	ScriptedPlans []controller.PlanProposal `json:"scriptedPlans" yaml:"scriptedPlans"`
	Controller    ControllerConfig          `json:"controller" yaml:"controller"`
}

type ControllerConfig struct {
	ReasoningEffort string `json:"reasoningEffort,omitempty" yaml:"reasoningEffort,omitempty"`
	Type            string `json:"type" yaml:"type"`
	ProfileID       string `json:"profileId,omitempty" yaml:"profileId,omitempty"`
	Provider        string `json:"provider,omitempty" yaml:"provider,omitempty"`
	Runtime         string `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	ModelIdentifier string `json:"modelIdentifier,omitempty" yaml:"modelIdentifier,omitempty"`
}

const DoctrineSnapshotVersion = 2

type Scenario struct {
	SnapshotVersion       int             `json:"snapshotVersion,omitempty"`
	ID                    string          `json:"id"`
	RulesVersion          string          `json:"rulesVersion"`
	RulesetID             string          `json:"rulesetId"`
	MaximumTicks          uint64          `json:"maximumTicks"`
	TickDuration          time.Duration   `json:"tickDuration"`
	DecisionIntervalTicks controller.Tick `json:"decisionIntervalTicks"`
	Map                   Map             `json:"map"`
	Ruleset               Ruleset         `json:"ruleset"`
	Factions              []FactionSetup  `json:"factions"`
}

type Unit struct {
	Scout                bool   `json:"scout,omitempty" yaml:"scout,omitempty"`
	PreferredTargetClass string `json:"preferredTargetClass,omitempty" yaml:"preferredTargetClass,omitempty"`
	ArmorPiercing        int64  `json:"armorPiercing,omitempty" yaml:"armorPiercing,omitempty"`
	RepairAmount         int64  `json:"repairAmount,omitempty" yaml:"repairAmount,omitempty"`
	RepairCost           int64  `json:"repairCost,omitempty" yaml:"repairCost,omitempty"`
	RepairCharges        int    `json:"repairCharges,omitempty" yaml:"repairCharges,omitempty"`
	HealRange            int    `json:"healRange,omitempty" yaml:"healRange,omitempty"`

	AttackMode          string    `json:"attackMode,omitempty"`
	AttackOutcome       string    `json:"attackOutcome,omitempty"`
	Class               string    `json:"class,omitempty" yaml:"class,omitempty"`
	MinimumRange        int       `json:"minimumRange,omitempty" yaml:"minimumRange,omitempty"`
	BuildingPriority    bool      `json:"buildingPriority,omitempty" yaml:"buildingPriority,omitempty"`
	BuildingDamageBonus int64     `json:"buildingDamageBonus,omitempty" yaml:"buildingDamageBonus,omitempty"`
	BonusAgainstClass   string    `json:"bonusAgainstClass,omitempty" yaml:"bonusAgainstClass,omitempty"`
	ClassDamageBonus    int64     `json:"classDamageBonus,omitempty" yaml:"classDamageBonus,omitempty"`
	HealAmount          int64     `json:"healAmount,omitempty" yaml:"healAmount,omitempty"`
	HealTargetID        string    `json:"healTargetId,omitempty"`
	HealTargetPosition  *Position `json:"healTargetPosition,omitempty"`

	ID                   string     `json:"id"`
	FactionID            string     `json:"factionId"`
	Type                 string     `json:"type"`
	Position             Position   `json:"position"`
	Health               int64      `json:"health"`
	MaxHealth            int64      `json:"maxHealth"`
	Attack               int64      `json:"attack"`
	Defense              int64      `json:"defense"`
	Speed                int64      `json:"speed"`
	Movement             int        `json:"movement"`
	Range                int        `json:"range"`
	Vision               int        `json:"vision"`
	Cost                 int64      `json:"cost"`
	ReadyAtTick          uint64     `json:"readyAtTick"`
	MovementPath         []Position `json:"movementPath,omitempty"`
	MovedFrom            *Position  `json:"movedFrom,omitempty"`
	AttackTargetID       string     `json:"attackTargetId,omitempty"`
	AttackTargetPosition *Position  `json:"attackTargetPosition,omitempty"`
}

// BuildingType describes additional constructible structures; legacy castle/economy
// stats remain in BuildingRules for persisted v1 scenarios.
type BuildingType struct {
	Trains     []string `json:"trains,omitempty" yaml:"trains,omitempty"`
	BuildRange int      `json:"buildRange,omitempty" yaml:"buildRange,omitempty"`
	ID         string   `json:"id" yaml:"id"`
	Kind       string   `json:"kind" yaml:"kind"`
	Health     int64    `json:"health" yaml:"health"`
	Defense    int64    `json:"defense" yaml:"defense"`
	Vision     int      `json:"vision" yaml:"vision"`
	Income     int64    `json:"income" yaml:"income"`
	Cost       int64    `json:"cost" yaml:"cost"`
	Attack     int64    `json:"attack,omitempty" yaml:"attack,omitempty"`
	Range      int      `json:"range,omitempty" yaml:"range,omitempty"`
}

// TrainingOrder is paid when queued; only the first order advances.
type TrainingOrder struct {
	UnitType       string `json:"unitType"`
	RemainingTicks uint64 `json:"remainingTicks"`
	TrainingTicks  uint64 `json:"trainingTicks"`
	QueuedAtTick   uint64 `json:"queuedAtTick"`
	Opening        bool   `json:"opening,omitempty"`
}

// BuildingProduction persists an AI-selected repeat order independently of strategy windows.
type BuildingProduction struct {
	Configured     bool   `json:"configured,omitempty"`
	UnitType       string `json:"unitType,omitempty"`
	Enabled        bool   `json:"enabled"`
	WaitingForGold bool   `json:"waitingForGold,omitempty"`
	// WaitingForUnitLimit pauses new starts, without charge, while the faction is at its unit cap.
	WaitingForUnitLimit bool `json:"waitingForUnitLimit,omitempty"`
	// WaitingForUpgrade pauses new starts, without charge, until the selected type is unlocked.
	WaitingForUpgrade bool `json:"waitingForUpgrade,omitempty"`
}

type Building struct {
	Production      *BuildingProduction `json:"production,omitempty"`
	Trains          []string            `json:"trains,omitempty"`
	BuildRange      int                 `json:"buildRange"`
	TrainingQueue   []TrainingOrder     `json:"trainingQueue,omitempty"`
	TrainingBlocked bool                `json:"trainingBlocked,omitempty"`

	AttackOutcome        string    `json:"attackOutcome,omitempty"`
	Attack               int64     `json:"attack,omitempty"`
	Range                int       `json:"range,omitempty"`
	ReadyAtTick          uint64    `json:"readyAtTick,omitempty"`
	AttackTargetID       string    `json:"attackTargetId,omitempty"`
	AttackTargetPosition *Position `json:"attackTargetPosition,omitempty"`

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
	OwnedUpgradeIDs    []string `json:"ownedUpgradeIds,omitempty"`
	FactionID          string   `json:"factionId"`
	Gold               int64    `json:"gold"`
	RecruitmentCursor  int      `json:"recruitmentCursor"`
	GoldEarned         int64    `json:"goldEarned"`
	GoldSpent          int64    `json:"goldSpent"`
	GoldLooted         int64    `json:"goldLooted"`
	UnitsStarted       int      `json:"unitsStarted"`
	UnitsRecruited     int      `json:"unitsRecruited"`
	UnitsLost          int      `json:"unitsLost"`
	BuildingsBuilt     int      `json:"buildingsBuilt"`
	BuildingsDestroyed int      `json:"buildingsDestroyed"`
}

// UnitDeath records a confirmed death for presentation during the current tick.
type UnitDeath struct {
	ID        string   `json:"id"`
	FactionID string   `json:"factionId"`
	Type      string   `json:"type"`
	Position  Position `json:"position"`
}

type State struct {
	UnitDeaths      []UnitDeath    `json:"unitDeaths,omitempty"`
	OpeningComplete bool           `json:"openingComplete,omitempty"`
	BattleID        string         `json:"battleId"`
	Tick            uint64         `json:"tick"`
	Factions        []FactionState `json:"factions"`
	Units           []Unit         `json:"units"`
	Buildings       []Building     `json:"buildings"`
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
	ContinuousProduction bool           `json:"continuousProduction,omitempty"`
	LegalBuildings       []BuildingType `json:"legalBuildings,omitempty"`
	RecruitmentSources   []string       `json:"recruitmentSources,omitempty"`
	MaximumBuildings     int            `json:"maximumBuildings,omitempty"`
	CurrentBuildings     int            `json:"currentBuildings,omitempty"`
	MaximumUnits         int            `json:"maximumUnits,omitempty"`
	CurrentUnits         int            `json:"currentUnits,omitempty"`

	FactionName     string                  `json:"factionName,omitempty"`
	Background      string                  `json:"background,omitempty"`
	Doctrine        string                  `json:"doctrine,omitempty"`
	Opening         bool                    `json:"opening"`
	LegalUpgrades   []Upgrade               `json:"legalUpgrades"`
	OwnedUpgradeIDs []string                `json:"ownedUpgradeIds"`
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
