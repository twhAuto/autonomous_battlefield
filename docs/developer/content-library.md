# Shared content and map authoring

Every live map uses one shared ruleset. The catalog embeds and validates:

- `backend/gamedata/battlefield.yaml`: shared ruleset ID, tick limit, tick duration,
  decision interval, fallback and scripted plans.
- `backend/gamedata/rulesets/battlefield.yaml`: shared starting gold, combat rules,
  building limits and content-library reference.
- `backend/gamedata/content/core-v5.yaml`: all units, upgrades, buildings and AI knowledge.
- `backend/gamedata/scenarios/*.yaml`: map ID and geometry only.

The shipped maps are `castle-siege` (15×11) and `frontier-siege` (25×17). The
former restricted ranged-skirmish entry duplicated Castle Siege geometry and is
removed. Map selection never filters available content. The API retains scenario
IDs and content-summary fields for clients; every summary reports the same shared
roster and upgrade catalog. The faction-rulesets endpoint returns one ruleset.

## Scenario restrictions

Maps no longer support content restrictions, timing, controller plans or ruleset
references. Those fields are rejected by strict YAML decoding. A new map file
contains only `id` and `map`, with authored rows, dimensions, castle spawns. Copy a shipped map and edit its geometry; all current and
future shared units and upgrades are available automatically.

Use `.` for ground, `#` for rocks, `f` for forest and `g` for gold deposits.
Construction sites are computed from building ranges and occupancy; map selection
does not change the shared caps or costs. Legacy `b` pads and `economySites` remain
readable for saved scenarios but do not restrict new construction.

## Versioning and runtime behavior

There is exactly one live ruleset and one active library reference. To change
content, publish its new library version and update the shared reference once.
Do not duplicate maps for each ruleset version. Libraries supply all units,
upgrades and valid non-castle structures; there are no selection lists.

The shared ruleset points to the library using `library.id`, `library.version`,
`library.castle` and `library.economy`. It owns starting gold, building limits and
combat percentages. Inline rulesets remain readable for self-contained fixtures.
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
The active v5 library uses Outpost, omits vision stats, and assigns `trains` unit-ID
arrays to five production kinds. Arrays must contain known, nonduplicated unit IDs;
nonproduction kinds cannot carry training rosters. New units need an assignment to
a production building to be trainable. Frozen older snapshots without rosters retain
castle/Barracks recruitment. New matches receive a free starting Barracks per faction.
Unit definitions support class, minimum attack range, building priority/damage,
class bonuses and healing. Specialist fields are `scout`, `preferredTargetClass`,
`armorPiercing`, `repairAmount`, `repairCost`, `repairCharges` and `healRange`.
A repair unit must be non-attacking support with positive cost/charges and no
healing or offensive capabilities. See [gameplay](../game/frontier-siege.md) for
precise behavior, prices and tradeoffs.

`knowledge.mechanics` is limited to 4096 UTF-8 bytes and each card field to 1024.
Provide one card per unit with `unitId`, `role`, `strengths`, `weaknesses` and
`guidance`. Facts come from definitions; advice is not a guaranteed matchup result.
The [AI decision contract](../architecture/ai-decisions.md#versioned-unit-knowledge)
describes bounded knowledge retrieval.

### Attack targeting capability

Optional `attackMode: tracking | position` defaults to tracking for old snapshots.
Catapults aim at a hex before unit turns and hit its current enemy occupant on their
speed-ordered turn; a moved target can evade. Retreat cancels the aimed shot.

## Upgrade tracks

Arsenal, Plating and Industry use `track`, `level` (1–3) and `requiresUpgradeId`.
Each track/level is unique; level I has no prerequisite and later levels require
the preceding level with a strictly greater price. Each entry is one purchase ID
with incremental effects: 20/50/100 gold buys total +2/+4/+6 bonuses.

Specializations use either `targetUnit` or `targetClass`, plus `exclusiveGroup`.
At most one choice in each group can be owned or ordered in a plan. Scoped fields
include attack/defense, `movementBonus`, `rangeBonus`, `healAmountBonus`,
`healRangeBonus` and `buildingDamageBonus`. Healing effects require healer targets;
economy bonuses remain faction-wide. Existing and future matching units receive
each effect once. The engine and AI submission validator enforce the same choices.

Run the [coverage gates](ci.md#coverage-gate-details), frontend build and document
link check when changing content. Use a new battle to exercise shared content;
saved battles retain their original definitions.
