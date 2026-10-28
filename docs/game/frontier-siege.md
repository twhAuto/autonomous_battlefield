# Frontier Siege

For the battle loop, tick order, production windows, and win conditions in one
place, see [Game rules](game-rules.md). This page owns the Frontier roster,
structures, maps, and balance probes.

Choose one of the [maps](#maps) in **Stage a battle → Combat map**. Every map uses
the same current roster, buildings, upgrades, starting gold and battle timing; only
the terrain differs. Every map has forests and gold deposits, with no reserved
building pads. Historical saved battles retain their frozen content definitions.

## Maps

| Map | ID | Size | Castles | Layout |
| --- | --- | --- | --- | --- |
| Castle Siege | `castle-siege` | 15×11 | `(1,5)`, `(13,5)` | Compact open field; two deposits per side inside castle build range |
| Frontier Siege | `frontier-siege` | 25×17 | `(2,8)`, `(22,8)` | Central approach and flanking routes around stone ridges; six deposits per side |
| River Fords | `river-fords` | 19×25, tall | `(2,12)`, `(16,12)` | A three-hex rock river splits the halves. Three fords, each three rows wide, cross it; the north and south fords each hold a contested deposit. Six deposits per side |
| Long March | `long-march` | 35×17, wide | `(2,8)`, `(32,8)` | A long field; rock outcrops split the centre into north, centre and south roads. Six deposits per side and two contested ones on the centre line |
| Highland Crown | `highland-crown` | 33×25, large | `(3,12)`, `(29,12)` | A hexagonal highland with rock corners. A rock-ringed plateau in the centre holds three contested deposits behind north and south gates. Six deposits per side |

Contested deposits sit on the centre column, the same walking distance from both
castles.

## Map and economy

Rocks block movement but ranged attacks use distance without line-of-sight
occlusion. Every map cell and entity is visible.
Forests grant troops +3 defense after armor piercing and prohibit construction;
gold deposits add +5 income per tick to an economy building placed on them.

Each faction starts with 220 gold and only its castle, and may maintain **sixteen non-castle buildings** and **40 units** (living plus paid in training),
including at most **four economy buildings**. Construction requires unoccupied
buildable terrain within a living active friendly building's range. Every pair of
buildings must be at least two hexes apart, including opposing structures.
Destroyed buildings release capacity and stop granting range. Economy activates
immediately; other structures activate next tick. Engineers can repair non-castle
buildings. There is no selling, demolition, or capture.

## Unit roster

| Unit | Gold | HP | Attack | Defense | Speed | Move | Range | Training ticks | Capability |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- | ---: | --- |
| Vanguard | 35 | 70 | 9 | 7 | 4 | 1 | 1 | 4 | Durable infantry |
| Striker | 25 | 42 | 14 | 3 | 8 | 2 | 1 | 2 | Mobile assault |
| Ranger | 30 | 30 | 11 | 2 | 6 | 1 | 3 | 3 | Ranged infantry |
| Catapult | 70 | 40 | 4 | 1 | 2 | 1 | 2–5 | 8 | +20 attack vs buildings |
| Battering Ram | 65 | 125 | 3 | 5 | 1 | 1 | 1 | 7 | +25 attack vs buildings |
| Pikeman | 30 | 55 | 8 | 4 | 4 | 1 | 1 | 3 | +12 attack vs mobile troops |
| Herbalist | 40 | 25 | 0 | 1 | 3 | 1 | Adjacent | 4 | Treat one soldier for 3 HP |
| Scout | 25 | 22 | 3 | 0 | 10 | 3 | 1 | 2 | Retreats from nearby enemies |
| Raider Cavalry | 45 | 45 | 9 | 2 | 9 | 3 | 1 | 5 | Prefers siege; +16 attack vs siege |
| Engineer | 50 | 28 | 0 | 1 | 3 | 1 | Adjacent | 5 | Repairs 4 HP; 2 gold; 12 charges |
| Crossbowman | 40 | 28 | 13 | 1 | 3 | 1 | 2 | 4 | Ignores up to 6 unit defense |
| Cannon | 220 | 80 | 14 | 3 | 2 | 1 | 2–6 | 12 | Requires Gunpowder; +36 attack vs buildings |
| Gunner | 120 | 38 | 24 | 2 | 3 | 1 | 2 | 6 | Requires Gunpowder; highest troop attack |
| Heavy Cavalry | 150 | 130 | 17 | 7 | 7 | 2 | 1 | 9 | Requires Knighthood; +8 attack vs infantry |

Speed determines turn order, not distance or attacks per tick. Units move or act
once per tick; recruits first act on the following tick. Bonus attack is added
before defense and the existing faction tactic modifiers. There is no splash
damage or friendly fire. Both factions see every battlefield action. Faction economic totals remain private.

Siege units prioritize defense towers, other non-castle buildings, then castles.
Within a priority group they approach by distance and stable ID, trying the next
structure when no firing position is reachable. An enemy in attack range is still
an available target when no building is in range. Catapults and Cannons attempt to
retreat when enemies are adjacent; a blocked retreat never permits an adjacent attack.
Screening and escort behavior remains autonomous, without player formation commands.

### Position attacks

Catapults, Cannons and Gunners attack a position rather than a unit. They aim
after economy and tactics, before unit turns, then hit that fixed hex on their own
turn (speed 2 for siege, 3 for Gunners), so faster units can leave before impact. Another living enemy at the hex can take the
hit; empty or friendly-only hexes miss. Siege target priority and building bonuses
still apply. Retreat cancels an aimed shot. Melee units and Rangers attack their
target immediately.

### Herbalists

Herbalists treat the soldier within treatment range (base 1 hex) missing the highest fraction of maximum
health; ties use unit ID. Healing cannot exceed maximum health. They cannot treat
themselves, other support units, siege engines, buildings, or dead troops. Otherwise
they approach reachable injured soldiers, preferring the shortest route, then the
largest missing fraction and ID. With no injured soldier reachable, they follow
a Vanguard (otherwise another soldier), preferring its friendly-castle side.
They hold if no route exists. Treatment replaces movement and attacking, and is
not an attack, so the minimum-damage rule never applies to it. Arsenal
does not grant attack or improve healing; Plating still adds defense.

### Scouts, cavalry, crossbows and repairs

Scouts are fast skirmishers. Before fighting, they attempt to retreat
from enemies within two hexes to a reachable position farther from
threats. If retreat is impossible, they use normal combat behavior.

Raider Cavalry prioritizes siege among targets in attack range, otherwise pursues
a reachable siege unit anywhere on the map before advancing normally. Pikemen's
anti-mobile bonus applies to both cavalry and Scouts. Crossbowmen subtract up to
6 from an enemy unit's defense before normal damage/tactic calculations; this
penetration never applies to buildings.

### Late-game units

Cannon, Gunner and Heavy Cavalry are locked until the faction owns their
[unlock upgrade](game-rules.md#late-game-unlocks): Gunpowder (1,000 gold) for
Cannon and Gunner, Knighthood (800 gold) for Heavy Cavalry. They train in existing
buildings and cost more gold and training time per unit, so they matter most once
both factions are fully upgraded and at the unit cap.

- **Cannon** (Siege Workshop) outranges castles, towers and Catapults at range 6.
  Its +36 building bonus breaks a tower in about three shots and a castle in about
  seven. It shares Catapult targeting, retreat and the siege specializations, and
  Raider Cavalry's +16 anti-siege bonus applies to it.
- **Gunner** (Archery Range) has the highest troop attack, 24, enough to wound fully
  plated Vanguards that Rangers barely scratch. It has no armor piercing, so defense
  still reduces its damage, and it also damages buildings. Its volleys use position
  attacks, so faster targets that move away are missed, and Rangers outrange it.
- **Heavy Cavalry** (Stables) is a durable mobile unit with +8 attack against
  infantry. As a mobile unit it takes Pikemen's +12 bonus, and Gunners hit it hard
  from range.

Engineers repair a damaged friendly siege unit or non-castle building for up to
4 HP. Each action consumes 2 gold and one of 12 lifetime charges, cannot exceed
maximum health, and respects reserve gold. Adjacent enemies interrupt the action.
They cannot attack, repair castles, revive dead entities, or replenish charges.
Targets are ordered by distance then stable ID; Engineers approach reachable
repair targets, otherwise follow an allied siege engine. Repair replaces movement
for that turn. Arsenal never grants Engineers an attack; Plating still applies.

## Structures and orders

| Structure | Gold | HP | Defense | Build range | Function |
| --- | ---: | ---: | ---: | ---: | --- |
| Castle (starting only) | 0 | 300 | 8 | 4 | +10 income/tick; Vanguard, Striker, Pikeman; attack 15, range 3 |
| Economy | 50 | 60 | 2 | 3 | +10 income/tick; +15 on a deposit |
| Defense Tower | 60 | 130 | 4 | 3 | Attack 15, range 3, targets enemy units |
| Outpost | 30 | 50 | 1 | 5 | Extends construction territory |
| Barracks | 90 | 100 | 3 | 3 | Vanguard, Striker, Pikeman |
| Archery Range | 90 | 100 | 3 | 3 | Ranger, Crossbowman, Gunner |
| Stables | 100 | 100 | 3 | 3 | Scout, Raider Cavalry, Heavy Cavalry |
| Siege Workshop | 120 | 100 | 3 | 3 | Catapult, Battering Ram, Engineer, Cannon |
| Infirmary | 80 | 100 | 3 | 3 | Herbalist |

Castles and towers act in stable building-ID order before unit turns. They use the same
distance/health/ID target tie breakers and faction damage modifiers as ordinary
attacks, award normal loot, and cannot attack buildings. At 60 gold, a tower has
more base HP per gold than any unit, making stationary defense economical.
Siege bonuses and the ability to bypass a fixed tower remain its counters.

A plan orders legal structures and selects one repeating unit per active training
building, including the castle for Barracks units. Construction is unavailable at
opening. Each enabled building pays for one unit at training start; insufficient
gold above reserveGold pauses starts until affordable. Off or type changes leave
the paid unit intact. Blocked exits retain that unit without further purchases.
Destroyed sources lose paid training without refund; deployed troops act next tick.
See [construction and training](game-rules.md#construction-and-training) for timing
and [OpenAPI](../architecture/openapi.yaml) for fields.

Scripted construction favors deposits, then locations nearer the opponent, choosing
separated sites for income, a tower and missing production types. Scripted templates
select a compatible unit for each newly active unconfigured source and retain
existing selections, with one exception: once their faction owns an unlock, they
move an enabled source from a classic type to a preferred late-game type it can
train. Disabled sources are never changed. AI factions receive scripted plans only
as fallbacks in their first two windows, before any unlock is affordable. The AI
can change its choices or disable individual sources.
Frozen pre-v7 battles retain their original batch queues.
Scripted baselines omit currently untrainable orders before submission. Once four
economy buildings, all five production kinds and one tower are built, default
construction stops, leaving free slots for explicit orders; destroyed types become eligible for replacement. Explicit orders take precedence; Outposts are an
alternative purchase for wider expansion. Opening fallback buys combat units in
deterministic round-robin order from the castle. Other unit types require
their own production building first.

## Three-level upgrades

| Level | Incremental gold cost | Total spent per track | Total bonus |
| --- | ---: | ---: | ---: |
| I | 160 | 160 | +2 |
| II | 400 | 560 | +4 |
| III | 800 | 1,360 | +6 |

Arsenal increases combat-unit attack, Plating increases unit defense, and Industry
increases income per economy building. Levels are ordered purchases, not research
timers. For example, `arsenal`, `arsenal_2`, `arsenal_3` buys all three if affordable.
The first unaffordable level stops that window's upgrade purchases; a later plan
can repeat the chain and skip owned levels without paying again.

Shared scripted plans spend on construction, then upgrades; enabled sources buy
units automatically afterward and between windows. They
order Industry, Arsenal and Plating at levels I and II, then Knighthood and
Gunpowder, then level III, and prefer Heavy Cavalry, Gunners and Cannons once
unlocked. Model controllers
choose their own priorities. Spectators see the highest owned level for each track,
and purchase events show the level and its incremental price.

Industry adds 2 gold per living economy building per tick per level. With one
building, incremental break-even times are 80/200/400 income collections; with two,
they are 40/100/200; with the maximum four, 20/50/100. Levels II and III therefore
pay back only with most economy slots filled or in long battles. These calculations assume the buildings
survive and exclude the time and gold spent constructing them. The compounding
economy is a balance consideration, not proof of a dominant strategy.

## Specializations

Each choice costs 480 gold. Choose at most one in each row for the battle; all
choices are available on every map. Bonuses apply to existing and future matching
units and stack with Arsenal, Plating and Industry where applicable.

| Unit role | Choice A | Choice B |
| --- | --- | --- |
| Vanguard | Bulwark: +3 defense | Assault: +4 attack |
| Ranger | Longshot: +1 range | Skirmish: +1 movement |
| Herbalist | Potency: +2 treatment (3 → 5 HP) | Reach: +1 treatment range (1 → 2 hexes) |
| Siege engines | Mobility: +1 movement | Breach: +8 building damage |

The opportunity cost is the alternative branch and 480 gold that could fund
recruits. There are no manual formations, research timers or branch refunds.

## Balance probes

`TestFrontierEqualBudgetMatchups` uses historical v2 fixtures and runs mirrored openings without later purchases.
The 130-gold comparison favors two Vanguards/two Rangers over one each of Vanguard,
Striker, Ranger, and Herbalist, despite treatment occurring. At 140 gold, two
Vanguards plus a Catapult defeat four Vanguards on both sides by attacking the
castle. The 150-gold Pikeman/Striker comparison is side-sensitive: mobile units can
reach a castle while opposing troops survive. These are balance probes, not
guaranteed matchup outcomes or evidence of tournament balance. Stable turn order,
spawn arrangement, routing, and castle pressure matter alongside damage bonuses.

Run `go test ./internal/battle/catalog -run TestFrontier -v` from `backend` to
repeat the probes. Tune balance through a new content version.

`TestFrontierUpgradeBalanceProbe` compares equal-budget openings using current
unit/upgrade definitions with historical batch recruitment explicitly enabled. It
does not measure the balance of continuous production. Training time and new terrain affect outcomes, so historical results
are not claims about the current balance. `TestFrontierThreeLevelCatalogAndScriptedBattle`
also checks shared content and scripted upgrade purchases.

`TestLateGameUnitsBreakUpgradedStalemate` runs the live ruleset with equal 8,000-gold
treasuries. Both sides build the same four training buildings and buy every general
upgrade level; one side also buys both unlocks and trains Gunners, Heavy Cavalry and
Cannons instead of Crossbowmen, Raider Cavalry and Catapults. The classic mirror
ends at tick 130, while the unlocked side wins from either map side by tick 94, and
an unlocked mirror ends by tick 81. `TestFrontierUpgradeBalanceProbe` compares a
level-I upgrade plus troops with an equal-cost army at the opening; both draw.
`TestScriptedBotsUnlockAndTrainLateGameUnits` runs the scripted bots on the live
ruleset: both fill four economy slots, buy both unlocks after level II, train Gunners
and Heavy Cavalry, and the battle ends with a winner (tick 258).

`TestScriptedBotsFinishEveryMap` plays the scripted bots on every map with each
faction on each side. Every battle must end with a winner before the 800-tick limit;
current results range from tick 169 (River Fords) to 369 (Castle Siege). Narrow
chokepoints let both armies hold a front: drafts with one- and two-row fords, or a
13-row Long March, reached the limit without a winner.
