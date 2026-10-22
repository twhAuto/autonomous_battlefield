# Game rules

| Field | Value |
| --- | --- |
| Status | Current: describes implemented engine behavior |
| Audience | Contributors and AI agents who need the rules in one place |
| Source of truth | `backend/internal/game` (engine); this page summarizes it |

This page is the single entry point for the battle rules. It links to the owning
documents for detail instead of repeating them:
[Frontier Siege](frontier-siege.md) for the roster, structures, and map, and the
[system architecture](../architecture/system-architecture.md#52-strategy-decision-window) for how
decisions are requested, stored, and timed out. All maps use the shared
`backend/gamedata/rulesets/battlefield.yaml` ruleset, `content/core-v7.yaml`
roster and `backend/gamedata/battlefield.yaml` timing/controller configuration.
Scenario files contain only map identity and geometry.

## Battle loop

A battle is a finite, deterministic simulation between **two factions** on an
authored hex map. Nobody controls units directly. Each faction's controller
(scripted or AI model) submits a **strategy plan** at each production window, and
units then act autonomously every tick according to fixed rules. Identical inputs
and plans always produce identical outcomes; the engine uses no randomness.

A battle is created with a requested tick count (at most the scenario's
`maximumTicks`, 800 for Frontier Siege) and runs one tick per `tickDuration` (1 s).

## Map and starting state

- Authored 15×11 and 25×17 hex maps with one castle per faction. All terrain,
  entities, routes and actions are visible. Rocks block movement, not ranged fire.
- Forests (`f`) are traversable and grant troops +3 defense after armor piercing;
  buildings cannot occupy them. Gold deposits (`g`) are traversable and grant an
  economy building on that hex +5 income per tick. Ground (`.`) has no bonus.
- Each castle has 300 HP, defense 8, construction range 4, earns 10 gold per tick, and attacks enemy units with 15 attack at range 3.
- Each faction starts with 220 gold and only its castle. Opening purchases enter the castle queue; there is no starting Barracks.

## Resources and upgrades

- **Gold** is the only resource. Income is collected every tick from the castle
  and each living economy building, and unspent gold carries forward.
- **Loot:** destroying an enemy unit or building awards 50% of its cost to the
  attacker's faction.
- **Upgrade purchases** run in order at the `upgrades` step of a plan's
  `spendOrder`. Owned purchase IDs are skipped free. The first purchase that
  would take gold below `reserveGold` stops upgrade buying for that window.
  Bonuses apply to current and future eligible entities. Industry applies
  from the next income collection. Historical saved battles retain their
  original one-time upgrade definitions.

In the **current shared ruleset**, each of those upgrade tracks has three sequential levels.
Levels I, II and III cost **20, 50 and 100 gold** respectively (170 total per
track), and each adds +2 for a cumulative +2/+4/+6 bonus. Buy prerequisites
first; multiple levels may be purchased in one production window when affordable.
Each level is owned once, and repeating a purchased level is free and adds no
further bonus. Skipping a prerequisite or requesting an unknown level rejects
the submitted plan before spending.

The effects and exceptions remain the same: Arsenal includes siege but does not
add attack or healing to Herbalists; Plating affects units, not buildings; Industry
affects economy buildings, not the castle. Upgrades last for that battle only.

Four independent specialization pairs cost 60 gold per choice: Vanguard defense
or attack, Ranger range or movement, Herbalist treatment strength or reach, and
siege movement or building damage. Each pair permits one choice per faction per
battle, with no switching/refund. These unit-scoped bonuses stack with general
upgrade levels. See the [specialization table](frontier-siege.md#specializations).

## Units and buildings

Eleven unit types (Vanguard, Striker, Ranger, Catapult, Battering Ram, Pikeman,
Herbalist, Scout, Raider Cavalry, Engineer and Crossbowman) and eight constructible structures (Economy, Defense Tower, Outpost, Barracks, Archery Range, Stables, Siege Workshop and Infirmary). Stats, costs, capabilities, targeting, and building limits (eight
non-castle buildings, at most two economy) are in
[Frontier Siege](frontier-siege.md#unit-roster).

## Production windows

Factions choose strategy, building production settings, construction and upgrades at
**decision windows**. Enabled buildings purchase individual troops automatically
between windows.
Engineers may also spend 2 gold on a repair action during a normal unit turn,
respecting the active plan's reserve. Each Engineer has 12 lifetime repair charges. Buildings and upgrades are purchased immediately. Each troop is paid for once when training starts. Continuous mode keeps one paid
unit per source; historical batch mode retains its FIFO queue.

- **Opening** (once, before tick 1): each faction spends its starting gold on
  upgrades and production selection only (batch recruitment in older battles). Every newly created battle has an opening; battles
  stored before doctrine snapshots (older `SnapshotVersion`) start without one. There is no income
  and no construction. Opening training begins at tick 1; a deployed troop first acts on the following tick.
- **Regular windows** run on the transition into every tick that is a multiple of
  `decisionIntervalTicks` (10: ticks 10, 20, 30, ...), as long as that tick does not
  exceed the requested tick count. A window exactly at the limit still runs.
- Windows change strategy, building production settings, construction and upgrades.
  Construction/upgrades follow `spendOrder`; automatic unit purchases happen afterward
  every tick, respecting `reserveGold`. The opening has no income or construction.
  Core v7 uses continuous production; frozen older rulesets retain batch recruitment
  (six units per window and twelve paid orders per source).

## Construction and training

Every living, active friendly building grants `buildRange` in hex distance.
Construct on unoccupied ground or a deposit within that range. All living buildings,
including enemy buildings and castles, must be at least two hexes apart: one
intervening hex. Historical `b` build-pad cells remain buildable but impassable;
new maps use ground and gold deposits. New structures are checked in order, so an earlier purchase can
block a later one. Illegal placements spend no gold. Destroyed buildings grant no
range and release their hex and building capacity. Economy buildings activate
immediately; other new structures activate next tick.

The castle is the only starting building and trains the same units as a Barracks.
It also fires at enemy units during the tower phase while its queue continues
training. Castles do not consume any of the eight constructible building slots.
Build additional Barracks for simultaneous training or forward deployment. The castle and
five constructible production buildings expose supported unit IDs as `trains`:

| Building | Units |
| --- | --- |
| Castle / Barracks | Vanguard, Striker, Pikeman |
| Archery Range | Ranger, Crossbowman |
| Stables | Scout, Raider Cavalry |
| Siege Workshop | Catapult, Battering Ram, Engineer |
| Infirmary | Herbalist |

The AI supplies `productionOrders` with `buildingId`, `unitType` and `enabled`.
Each enabled building repeatedly trains its selected type, paying for exactly one
unit when training starts. There is one paid unit in progress and no prepaid batch.
The unit's `trainingTicks` starts advancing on the tick after purchase. A successful
deployment can buy the next unit that same tick; its training advances next tick.
Purchases follow building ID order after income, construction/upgrades and training.
Insufficient gold above `reserveGold` sets `production.waitingForGold`; the building
stays enabled and retries each tick. No purchase occurs while a paid unit is present.

Setting `enabled: false` stops new starts but lets the paid unit finish. A different
`unitType` applies to the next purchase. An off order may omit `unitType` to retain
the selection. Omitted building orders keep existing settings. New training buildings
start off and can be configured at a decision window once active. Castles use this
same system; opening requires explicit production orders, and `[]` leaves them off.

A ready unit waits at zero remaining ticks with `trainingBlocked: true` if every
adjacent traversable hex is occupied. The next training phase retries the exit in
deterministic neighbor order, without stacking or charging again. Destroying a
source discards its paid unit without refund. A deployed unit acts next tick;
opening/later unit counters increment on deployment. Frozen core v6 and earlier
retain their FIFO queues, six purchases per window and twelve-order source limit.

Production settings and training progress are persisted in `Building` state and cloned with snapshots. Viewers see all living sources in the building production panel; only the AI changes settings.
The engine resumes a live paused match in memory; a new worker attempt replays its
saved scenario and decisions, rebuilding the same queue progression. Legacy unit
definitions without training times default to one tick and building ranges to 3.
Frozen content without authored `trains` rosters retains castle/Barracks recruitment
and does not add starting Barracks. Frozen core v5 scenarios retain their free
starting Barracks and passive, nontraining castles. Core v6 saves `castleTrains`,
`castleAttack` and `castleRange` in building rules; no free Barracks is added when
the castle has an authored roster. Core v7 sets `ruleset.continuousProduction: true`; absent/false preserves the older purchasing behavior.
Historical stored states retain their data; replaying them uses the current engine
rules, so pre-change transition hashes are not preserved. Before deploying this
overhaul, finish all queued/running/paused battles on the old engine and prevent
new battle creation during the cutover; see the [release precondition](../operations/deployment.md#gameplay-rules-cutover).

## Plan submission

At each window the engine asks every faction's controller for a plan, based on a
frozen observation of the entire battlefield, including enemy troops, structures,
and training queues. The opponent's gold, owned upgrades and aggregate production
totals remain private in controller observations; authorized browser viewers see
both factions' totals in the full battle snapshot:

- A **scripted** controller submits its deterministic plan immediately.
- A **model** controller receives the observation through MCP tools and must
  submit a valid plan with `submit_strategy_plan` before the engine's decision
  timeout (`decisionTimeout`, 2 minutes in `backend/config.yaml`). Invalid plans
  are rejected with validation errors; the model may correct them within its
  submission limit.
- If no valid plan arrives in time, the engine applies a **fallback**: at the
  opening, a round-robin through the non-healing units in ID order, buying one of
  each affordable type until six are bought or none is affordable; the scenario's scripted
  baseline at the first regular window, and the faction's previous plan after that.

A plan sets tactical `actionWeights` (attack vs. defend), spending (recruitment,
construction, and upgrade orders, `spendOrder`, `reserveGold`), and optional
`targetBonuses`. Plans cannot be applied outside a window. The plan fields are the
`StrategyPlan` schema in the [OpenAPI contract](../architecture/openapi.yaml); models
submit them through the MCP tool schema.

## Tick order

Each tick transition runs these steps in order (`AdvanceWithPlans` in
`backend/internal/game/match.go`):

1. **Apply plans** if this transition is a production window.
2. **Advance the tick** and clear the previous tick's action markers.
3. **Economy**, per faction in scenario faction order: collect income; at a
   window, also spend in the plan's `spendOrder`.
4. **Training**, in building-ID order: advance the paid unit (legacy queue head), then release
   at most one unit per building onto the first free traversable neighbor. A ready
   unit waits when all exits are blocked without another charge. In continuous
   mode, empty enabled sources then buy their next unit in building-ID order.
5. **Tactics**, per faction: attack or defend, comparing weighted projected power
   (the sum of attack of living non-support units) for each side, plus target bonuses.
6. **Aim position attacks:** Catapults fix their target hex before anyone moves.
7. **Towers** act in stable building-ID order.
8. **Units** act one at a time, fastest first, ties broken by unit ID. Speed sets
   turn order, not distance or attacks per tick. Units recruited this tick wait
   until the next. Each unit either treats (Herbalist), retreats (units with a
   minimum range, i.e. Catapults, when an enemy is adjacent and an escape route
   exists), fires at its aimed hex, attacks a target in range, or moves toward
   its objective. For most units that is a hex within attack range of the enemy
   castle; when occupied hexes block every castle approach, the unit heads for a
   reachable attack position around a living enemy unit or building instead, so a
   crowded castle perimeter cannot freeze both armies. Siege units head for
   structures in priority order and Herbalists follow allied soldiers (see
   [Frontier Siege](frontier-siege.md#unit-roster)). Routes never cross occupied
   or impassable hexes.
9. **Completion check** (see Winning).

## Combat

Damage = (attack + any class or siege bonus) - effective target defense, never below 0.
For troops, armor piercing reduces base defense first, then forest cover adds 3. Buildings receive no forest cover.
Then:

- an attacker whose faction is **defending** deals 80% of that;
- a target whose faction is **defending** takes 70% of that;
- the result is at least 1 (minimum damage).

There is no splash damage or friendly fire. Units and buildings at 0 HP are
removed from the battlefield display, and the attacker's faction collects loot.
Destroyed buildings remain in state for result and history references, but no longer
occupy tiles, grant range or accept orders; their queues are discarded.

## Winning

- **Castle destroyed:** the battle ends immediately when a castle falls, even if
  other units have not acted yet that tick. The other faction wins (reason
  `castle_destroyed`).
- **Tick limit:** if both castles stand when the requested tick count is reached,
  the battle ends in a **draw** with no winner (reason `tick_limit`).

Separately from the rules, operators can stop a battle, and unfinished battles
expire (fail with `battle_timeout`) one hour after creation. See the
[API reference](../architecture/api.md#status-and-controls) for battle status and controls.

## Not supported

These are not supported. Adding any of them changes the rules above:

- more than two factions (creation, spawns, movement, and winner rules assume two);
- direct player commands, formations, or unit control during a battle;
- line-of-sight occlusion for ranged fire;
- splash damage, friendly fire, or randomness of any kind;
- selling, demolition, capture, or castle repair;
- real-time push updates (clients poll);
- seeded or procedurally generated maps.
