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
decisions are requested, stored, and timed out. Values below are for the
`frontier-siege-v2` scenario (`backend/gamedata/scenarios/frontier-siege-v2.yaml`,
ruleset `backend/gamedata/rulesets/castle-siege-v2.yaml`, content
`backend/gamedata/content/core-v2.yaml`).

## Battle loop

A battle is a finite, deterministic simulation between **two factions** on an
authored hex map. Nobody controls units directly. Each faction's controller
(scripted or AI model) submits a **strategy plan** at each production window, and
units then act autonomously every tick according to fixed rules. Identical inputs
and plans always produce identical outcomes; the engine uses no randomness.

A battle is created with a requested tick count (at most the scenario's
`maximumTicks`, 800 for Frontier Siege) and runs one tick per `tickDuration` (1 s).

## Map and starting state

- An authored 25×17 hex map with one castle per faction and six reserved
  construction sites per side. Stone blocks movement but not ranged fire or vision.
- Each castle has 300 HP, defense 8, vision 4, and earns 10 gold per tick.
- Each faction starts with 220 gold and no units until its opening purchase.

## Resources and upgrades

- **Gold** is the only resource. Income is collected every tick from the castle
  and each living economy building, and unspent gold carries forward.
- **Loot:** destroying an enemy unit or building awards 50% of its cost to the
  attacker's faction.
- **Upgrades** cost 20 gold each and are owned once: Arsenal (+2 attack), Plating
  (+2 defense), Industry (+2 economy-building income). A plan's upgrade orders run
  in order at the `upgrades` step of its `spendOrder`; an upgrade already owned is
  skipped at no cost, and the first one that would take gold below `reserveGold`
  stops upgrade buying for that window. Attack and defense bonuses apply at once to
  living units and to future recruits (Herbalists gain no attack); the income bonus
  applies to existing and future economy buildings from the next collection.

## Units and buildings

Seven unit types (Vanguard, Striker, Ranger, Catapult, Battering Ram, Pikeman,
Herbalist) and four structures (Economy, Defense Tower, Watchtower, Forward
Barracks). Stats, costs, capabilities, targeting, and building limits (four
non-castle buildings, at most two economy) are in
[Frontier Siege](frontier-siege.md#unit-roster).

## Production windows

Factions spend gold only at **production windows**. All purchases for a window
happen at once; there is no persistent production queue, and unfilled purchases
are not carried forward.

- **Opening** (once, before tick 1): each faction spends its starting gold on
  upgrades and recruitment only. Every newly created battle has an opening; battles
  stored before doctrine snapshots (older `SnapshotVersion`) start without one. There is no income
  and no construction. Opening recruits first act at tick 1.
- **Regular windows** run on the transition into every tick that is a multiple of
  `decisionIntervalTicks` (10: ticks 10, 20, 30, ...), as long as that tick does not
  exceed the requested tick count. A window exactly at the limit still runs.
- Per window, a faction may request **at most six units** in total across all
  recruitment sources. Spending follows the plan's `spendOrder` (upgrades,
  construction, recruitment) and never goes below the plan's `reserveGold`.
  Recruitment stops at the first unaffordable order or blocked castle spawn.

## Plan submission

At each window the engine asks every faction's controller for a plan, based on a
frozen, fog-of-war observation of that faction's view. It shows enemy units and
buildings only inside the faction's vision (enemy castles are always shown) and
never the opponent's gold, upgrades or production totals:

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
4. **Tactics**, per faction: attack or defend, comparing weighted projected power
   (the sum of attack of living non-support units) for each side, plus target bonuses.
5. **Aim position attacks:** Catapults fix their target hex before anyone moves.
6. **Towers** act in stable building-ID order.
7. **Units** act one at a time, fastest first, ties broken by unit ID. Speed sets
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
8. **Completion check** (see Winning).

## Combat

Damage = (attack + any class or siege bonus) - target defense, never below 0.
Then:

- an attacker whose faction is **defending** deals 80% of that;
- a target whose faction is **defending** takes 70% of that;
- the result is at least 1 (minimum damage).

There is no splash damage or friendly fire. Units and buildings at 0 HP are
removed, and the attacker's faction collects loot.

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
- line-of-sight occlusion for ranged fire or vision;
- splash damage, friendly fire, or randomness of any kind;
- selling, demolition, capture, or repair of buildings;
- persistent production queues between windows;
- real-time push updates (clients poll);
- seeded or procedurally generated maps.
