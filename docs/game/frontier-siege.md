# Frontier Siege

For the battle loop, tick order, production windows, and win conditions in one
place, see [Game rules](game-rules.md). This page owns the Frontier roster,
structures, map, and balance probes.

Choose `frontier-siege-v2` in **Stage a battle → Combat scenario**. It uses
`castle-siege-v2` and `core` content version `2`; the v1 scenarios remain
available with their original three-unit roster. Both scripted and model
controllers can play it.

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

Herbalists treat the adjacent soldier missing the highest fraction of maximum
health; ties use unit ID. Healing cannot exceed maximum health. They cannot treat
themselves, other support units, siege engines, buildings, or dead troops. Otherwise
they approach reachable injured soldiers, preferring the shortest route, then the
largest missing fraction and ID. With no injured soldier reachable, they follow
a Vanguard (otherwise another soldier), preferring its friendly-castle side.
They hold if no route exists. Treatment replaces movement and attacking. Arsenal
does not grant attack or improve healing; Plating still adds defense.

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
described in the [API reference](../architecture/api.md#expanded-battlefield-data).

Scripted Frontier plans build rear income, a forward tower, and a forward Barracks
as budget permits, then use an active forward source for reinforcements. They
recruit mixed siege/infantry/support forces. Explicit model orders take precedence
over these defaults. Watchtowers are an alternative strategic purchase available
to model plans. Opening fallback buys combat units in deterministic round-robin
order and excludes Herbalists; explicit opening orders may buy them.

## Balance probes

`TestFrontierEqualBudgetMatchups` runs mirrored openings without later purchases.
The 130-gold comparison favors two Vanguards/two Rangers over one each of Vanguard,
Striker, Ranger, and Herbalist, despite treatment occurring. At 140 gold, two
Vanguards plus a Catapult defeat four Vanguards on both sides by attacking the
castle. The 150-gold Pikeman/Striker comparison is side-sensitive: mobile units can
reach a castle while opposing troops survive. These are balance probes, not
guaranteed matchup outcomes or evidence of tournament balance. Stable turn order,
spawn arrangement, routing, and castle pressure matter alongside damage bonuses.

Run `go test ./internal/battle/catalog -run TestFrontier -v` from `backend` to
repeat the probes. Tune balance through a new content version.
