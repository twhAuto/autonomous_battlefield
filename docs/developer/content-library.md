# Shared content and map authoring

Every live map uses one shared ruleset. The catalog embeds and validates:

- `backend/gamedata/battlefield.yaml`: shared ruleset ID, tick limit, tick duration,
  decision interval, fallback and scripted plans.
- `backend/gamedata/rulesets/battlefield.yaml`: shared starting gold, combat rules,
  unit and building limits, default gold per mine and content-library reference.
- `backend/gamedata/content/core-v9.yaml`: all units, upgrades, buildings and AI knowledge.
- `backend/gamedata/scenarios/*.yaml`: map ID, geometry and gold per mine only.

The shipped maps are `castle-siege` (15×11), `frontier-siege` (25×17),
`river-fords` (19×25), `long-march` (35×17) and `highland-crown` (33×25); see
[maps](../game/frontier-siege.md#maps). Map selection never filters available content. The API retains scenario
IDs and content-summary fields for clients; every summary reports the same shared
roster and upgrade catalog. The faction-rulesets endpoint returns one ruleset.

## Scenario restrictions

Maps carry no content restrictions, timing, controller plans or ruleset
references; strict YAML decoding rejects those fields. A new map file
contains only `id` and `map`, with authored rows, dimensions, castle spawns and an
optional `map.goldMineReserve` (gold per mine, 0–1,000,000; 0 uses the ruleset's
`buildings.goldMineReserve`). Copy a shipped map and edit its geometry; all current
and future shared units and upgrades are available automatically.

Use `.` for ground, `#` for rocks, `f` for forest and `g` for gold mines. A map's
richness is its mine count times its reserve, so set the reserve with the layout:
scarce gold at chokepoints lets both sides hold a front once the mines are empty.
Construction sites are computed from building ranges and occupancy; map selection
does not change the shared caps or costs. Legacy `b` pads and `economySites` remain
readable for saved scenarios but do not restrict new construction.

`game.ValidateMap` requires odd dimensions (width 15–41, height 11–31), rows that
read the same mirrored left to right, mirrored castle spawns with at least four
traversable neighbors, and a route between the castles. Catalog tests also require
forests, gold mines with four traversable neighbors, and no pads; at least two mines
per castle inside its build range but not adjacent to it; more mines beyond that
range than inside it, including at least one contested mine the same distance from
both castles; no mine beside a castle or another mine, because no building may stand
beside a mine with gold left; and a scarcest map with under half the richest map's
gold. Odd rows sit half a
hex to the right, so a mirrored row is not an exact reflection: keep a mine on a row
of the same parity as the castles' row so both sides walk the same distance to it,
and double a rock wall on odd rows where a single mirrored wall would leave a gap.
`TestScriptedBotsFinishEveryMap` plays scripted bots on every map from both sides
and fails if a battle reaches the tick limit; narrow chokepoints made earlier drafts
stall.

## Versioning and runtime behavior

There is exactly one live ruleset and one active library reference. To change
content, publish its new library version and update the shared reference once.
Do not duplicate maps for each ruleset version. Libraries supply all units,
upgrades and valid non-castle structures; there are no selection lists.

The shared ruleset points to the library using `library.id`, `library.version`,
`library.castle` and `library.economy`. It owns starting gold, building limits
(`buildings.maximumBuildingCount` and `maximumEconomyCount`, each 0–16; 0 is
unlimited), the default gold per mine (`buildings.goldMineReserve`, 0–1,000,000), the
per-faction `maximumUnitCount` (0–200; 0 is unlimited), combat percentages and
`continuousProduction`. A positive `goldMineReserve` turns every `g` hex into a
finite gold mine that economy buildings require; without it, `maximumEconomyCount: 0`
offers no economy buildings. The live ruleset v10 sets no building limits, 50 units
and 2,000 gold per mine, and references core v9, which includes the late-game
unlocks.
`continuousProduction: true` enables continuous AI-selected unit production; absent
or false selects the batch purchases of frozen older battles. The archived `content/core-v8.yaml` and `content/core-v7.yaml` are the authored
references for earlier active libraries, and `content/core-v6.yaml` for the
castle-training/attacking version with batch queues. They are not the active library
and are not needed to replay self-contained saved snapshots. Inline rulesets remain readable for self-contained fixtures.
Content IDs/versions allow letters, digits, dashes and underscores. Invalid stats,
unknown fields, missing files, multiple live rulesets, duplicate IDs or invalid
plans fail catalog startup.

Battle creation freezes the complete resolved scenario, including map, rules,
content digest, timing and plans, in PostgreSQL JSON. Workers replay that snapshot;
there is no lookup of a historical live catalog and no database migration. Older
v1/v2 authored files exist only under catalog test fixtures, where their original content/snapshot hashes remain pinned. Gameplay transition
hashes now reflect open-map, range and queue rules; persisted historical state is
not rewritten, but replay uses the current engine. Finish in-flight battles before
the [gameplay cutover](../operations/deployment.md#gameplay-rules-cutover); a worker
restart otherwise re-simulates old inputs with changed construction/training rules.

## Expanded content fields

Unit definitions include `trainingTicks` (1–1000; missing legacy values use 1).
Building definitions include `buildRange` (1–10; missing legacy values use 3).
The active v9 library uses Outpost, omits vision stats, and assigns `trains` unit-ID
arrays to the castle and five constructible production kinds. Arrays contain known
unit IDs without internal duplicates; a unit belongs to at most one constructible
production kind, while the castle may share that roster. Economy, towers and Outposts
cannot carry training rosters. New units need an assignment to be trainable. Castle
`trains`, `attack` and `range` resolve to `buildings.castleTrains`, `castleAttack` and
`castleRange`, saved with each scenario. Attack and range must both be positive or
both zero. Current castles share the Barracks roster and attack for 15 at range 3;
there is no starting Barracks. Frozen core v5 scenarios without castle rosters keep
their starting Barracks and passive castles. Older snapshots without any rosters
retain unrestricted castle/Barracks recruitment. In continuous mode, the AI sets
`productionOrders` per active source; units charge individually at training start,
wait for gold above the reserve and hold blocked exits. Disabling or changing type
preserves the paid unit. Viewers see both sides; faction decision contexts omit
opponent production settings while retaining visible paid training progress.
Unit definitions support class, minimum attack range, building priority/damage,
class bonuses and healing. Specialist fields are `scout`, `preferredTargetClass`,
`armorPiercing`, `repairAmount`, `repairCost`, `repairCharges` and `healRange`.
A repair unit must be non-attacking support with positive cost/charges and no
healing or offensive capabilities. Optional `requiresUpgradeId` locks a unit until
the faction owns that upgrade; the ID must name an upgrade in the same library, and
ruleset validation rejects unknown references. Give every training building at least
one type without an unlock so scripted plans can configure it. See [gameplay](../game/frontier-siege.md) for
precise behavior, prices and tradeoffs.

`knowledge.mechanics` is limited to 4096 UTF-8 bytes and each card field to 1024.
Provide one card per unit with `unitId`, `role`, `strengths`, `weaknesses` and
`guidance`. Facts come from definitions; advice is not a guaranteed matchup result.
The [AI decision contract](../architecture/ai-decisions.md#versioned-unit-knowledge)
describes bounded knowledge retrieval.

### Attack targeting capability

Optional `attackMode: tracking | position` defaults to tracking for old snapshots.
Catapults, Cannons and Gunners aim at a hex before unit turns and hit its current
enemy occupant on their speed-ordered turn; a moved target can evade. Retreat cancels
the aimed shot.

## Upgrade tracks

Arsenal, Plating and Industry use `track`, `level` (1–3) and `requiresUpgradeId`.
Each track/level is unique; level I has no prerequisite and later levels require
the preceding level with a strictly greater price. Each entry is one purchase ID
with incremental effects: 160/400/800 gold buys total +2/+4/+6 bonuses.
Unlock upgrades (`gunpowder`, `knighthood`) are plain one-time entries with no
track, scope or bonus; units reference them through `requiresUpgradeId`.

Specializations use either `targetUnit` or `targetClass`, plus `exclusiveGroup`.
At most one choice in each group can be owned or ordered in a plan. Scoped fields
include attack/defense, `movementBonus`, `rangeBonus`, `healAmountBonus`,
`healRangeBonus` and `buildingDamageBonus`. Healing effects require healer targets;
economy bonuses remain faction-wide. Existing and future matching units receive
each effect once. The engine and AI submission validator enforce the same choices.

Run the [coverage gates](ci.md#coverage-gate-details), frontend build and document
link check when changing content. Use a new battle to exercise shared content;
saved battles retain their original definitions.
