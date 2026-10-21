# Frontier Siege

For the battle loop, tick order, production windows, and win conditions in one
place, see [Game rules](game-rules.md). This page owns the Frontier roster,
structures, map, and balance probes.

Choose **Frontier Siege** (`frontier-siege`) or **Castle Siege** (`castle-siege`)
in **Stage a battle → Combat map**. Both use the same current roster, buildings,
upgrades, starting gold and battle timing. Only geometry and construction sites
differ. The compact Castle Siege layout is 15×11 with two sites per side;
Frontier Siege is 25×17 with six. Historical saved battles keep their frozen rules,
without exposing separate old rulesets in the live catalog.

## Map and economy

The authored 25×17 map has castles at `(2,8)` and `(22,8)`, a wide central
approach, and upper/lower flanking routes around broken stone ridges. Stone blocks
movement, but retains the existing rules for ranged fire and vision: neither uses
line-of-sight occlusion. Siege therefore depends on distance and screening troops.

Each side has six reserved construction sites. The left side has rear sites at
`(4,3)` and `(4,13)`, midfield sites at `(7,5)` and `(7,11)`, and forward sites at
`(9,7)` and `(9,9)`. The right side mirrors these across the map. Every site has
at least four adjacent ground cells for melee siege and recruitment access.

Each faction starts with 220 gold and may maintain **four non-castle buildings**,
including at most **two economy buildings**. All structure types compete for the
same sites and gold. Destroyed buildings release their site and capacity. There
is no selling, demolition, capture, or repair. Watchtower vision is immediate;
tower attacks and Barracks recruitment activate on the tick after construction.

## Unit roster

| Unit | Gold | HP | Attack | Defense | Speed | Move | Range | Vision | Capability |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- | ---: | --- |
| Vanguard | 35 | 70 | 9 | 7 | 4 | 1 | 1 | 2 | Durable infantry |
| Striker | 25 | 42 | 14 | 3 | 8 | 2 | 1 | 3 | Mobile assault |
| Ranger | 30 | 30 | 11 | 2 | 6 | 1 | 3 | 4 | Ranged infantry |
| Catapult | 70 | 40 | 4 | 1 | 2 | 1 | 2–5 | 5 | +20 attack vs buildings |
| Battering Ram | 65 | 125 | 3 | 5 | 1 | 1 | 1 | 2 | +25 attack vs buildings |
| Pikeman | 30 | 55 | 8 | 4 | 4 | 1 | 1 | 3 | +12 attack vs mobile troops |
| Herbalist | 40 | 25 | 0 | 1 | 3 | 1 | Adjacent | 3 | Treat one soldier for 3 HP |
| Scout | 25 | 22 | 3 | 0 | 10 | 3 | 1 | 7 | Retreats from nearby enemies |
| Raider Cavalry | 45 | 45 | 9 | 2 | 9 | 3 | 1 | 4 | Prefers siege; +16 attack vs siege |
| Engineer | 50 | 28 | 0 | 1 | 3 | 1 | Adjacent | 3 | Repairs 4 HP; 2 gold; 12 charges |
| Crossbowman | 40 | 28 | 13 | 1 | 3 | 1 | 2 | 3 | Ignores up to 6 unit defense |

Speed determines turn order, not distance or attacks per tick. Units move or act
once per tick; recruits first act on the following tick. Bonus attack is added
before defense and the existing faction tactic modifiers. There is no splash
damage or friendly fire. A faction's view omits action targets outside its
vision; spectators see every action.

Siege units prioritize defense towers, other non-castle buildings, then castles.
Within a priority group they approach by distance and stable ID, trying the next
structure when no firing position is reachable. An enemy in attack range is still
an available target when no building is in range. Catapults attempt to retreat
when enemies are adjacent; a blocked retreat never permits an adjacent attack.
Screening and escort behavior remains autonomous, without player formation commands.

### Catapult targeting

Catapults attack a position rather than a unit. They aim after economy and
tactics, before unit turns, then hit that fixed hex on their speed-2 turn, so
faster units can leave before impact. Another living enemy at the hex can take the
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

Scouts reveal a wide area with vision 7. Before fighting, they attempt to retreat
from enemies within two hexes to a reachable position farther from visible
threats. If retreat is impossible, they use normal combat behavior.

Raider Cavalry prioritizes siege among targets in attack range, otherwise pursues
a reachable siege unit within its own vision before advancing normally. Pikemen's
anti-mobile bonus applies to both cavalry and Scouts. Crossbowmen subtract up to
6 from an enemy unit's defense before normal damage/tactic calculations; this
penetration never applies to buildings.

Engineers repair a damaged friendly siege unit or non-castle building for up to
4 HP. Each action consumes 2 gold and one of 12 lifetime charges, cannot exceed
maximum health, and respects reserve gold. Adjacent enemies interrupt the action.
They cannot attack, repair castles, revive dead entities, or replenish charges.
Targets are ordered by distance then stable ID; Engineers approach reachable
repair targets, otherwise follow an allied siege engine. Repair replaces movement
for that turn. Arsenal never grants Engineers an attack; Plating still applies.

## Structures and orders

| Structure | Gold | HP | Defense | Vision | Function |
| --- | ---: | ---: | ---: | ---: | --- |
| Economy | 50 | 60 | 2 | 2 | +10 income each tick |
| Defense Tower | 80 | 130 | 4 | 4 | Attack 12, range 3, targets enemy units |
| Watchtower | 30 | 50 | 1 | 6 | Information without income or attacks |
| Forward Barracks | 90 | 100 | 3 | 3 | Additional recruitment source |

Towers act in stable building-ID order before unit turns. They use the same
distance/health/ID target tie breakers and faction damage modifiers as ordinary
attacks, award normal loot, and cannot attack buildings.

A plan can order any structure on one of its free construction sites and recruit
from the castle or from a ready Forward Barracks. Construction is unavailable at
the opening. A faction may request at most six units per production window across
all sources. Barracks place recruits on adjacent free ground with the same costs
and reserve. An unavailable source or a blocked Barracks order is skipped without
substitution; a blocked castle spawn or the first unaffordable order stops the
remaining purchases. Unfilled purchases are not queued. The plan fields are
the `StrategyPlan` schema in the [OpenAPI contract](../architecture/openapi.yaml).

Shared scripted plans build rear income, a forward tower, and a forward Barracks
as budget permits, then use an active forward source for reinforcements when the map has enough sites. They
recruit mixed siege/infantry/support forces. Explicit model orders take precedence
over these defaults. Watchtowers are an alternative strategic purchase available
to model plans. Opening fallback buys combat units in deterministic round-robin
order and excludes Herbalists and Engineers; explicit opening orders may buy them.

## Three-level upgrades

| Level | Incremental gold cost | Total spent per track | Total bonus |
| --- | ---: | ---: | ---: |
| I | 20 | 20 | +2 |
| II | 50 | 70 | +4 |
| III | 100 | 170 | +6 |

Arsenal increases combat-unit attack, Plating increases unit defense, and Industry
increases income per economy building. Levels are ordered purchases, not research
timers. For example, `arsenal`, `arsenal_2`, `arsenal_3` buys all three if affordable.
The first unaffordable level stops that window's upgrade purchases; a later plan
can repeat the chain and skip owned levels without paying again.

Shared scripted plans spend on construction, then upgrades, then recruitment. They
order Industry, Arsenal and Plating at each successive level. Model controllers
choose their own priorities. Spectators see the highest owned level for each track,
and purchase events show the level and its incremental price.

Industry adds 2 gold per living economy building per tick per level. With one
building, incremental break-even times are 10/25/50 income collections; with two,
they are 5/13/25 collections (rounded up). These calculations assume the buildings
survive and exclude the time and gold spent constructing them. The compounding
economy is a balance consideration, not proof of a dominant strategy.

## Specializations

Each choice costs 60 gold. Choose at most one in each row for the battle; all
choices are available on every map. Bonuses apply to existing and future matching
units and stack with Arsenal, Plating and Industry where applicable.

| Unit role | Choice A | Choice B |
| --- | --- | --- |
| Vanguard | Bulwark: +3 defense | Assault: +4 attack |
| Ranger | Longshot: +1 range | Skirmish: +1 movement |
| Herbalist | Potency: +2 treatment (3 → 5 HP) | Reach: +1 treatment range (1 → 2 hexes) |
| Siege engines | Mobility: +1 movement | Breach: +8 building damage |

The opportunity cost is the alternative branch and 60 gold that could fund
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

`TestFrontierUpgradeBalanceProbe` compares equal 170-gold openings with the current shared rules:
two Vanguards and one Ranger plus Arsenal II or Plating II, against four
Vanguards and one Ranger. With no later purchases, Arsenal II wins from the
left but loses from the right; Plating II loses in both orientations. These
limited probes demonstrate the troop-versus-upgrade tradeoff and side sensitivity,
not tournament balance. `TestFrontierThreeLevelCatalogAndScriptedBattle` also
checks the shared catalog and scripted upgrade purchases.
