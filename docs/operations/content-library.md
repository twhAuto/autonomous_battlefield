# Content libraries and scenario selections

Author reusable unit, upgrade, and building definitions in
`backend/content/<id>-v<version>.yaml`. The shipped `core-v1.yaml` contains the
three starter units, their knowledge cards, three upgrades, and castle/economy
building definitions. The version pins the whole library; unit IDs remain stable
within it. Buildings support the existing `castle` and `economy` engine roles.
New abilities or building roles require engine support.

A ruleset references a library and selects content while retaining ownership of
starting gold, design budget, combat percentages, and economy-building limits:

```yaml
id: example-v1
version: "1"
startingGold: 180
library:
  id: core
  version: "1"
  units: [vanguard, ranger]
  upgrades: [plating, industry]
  castle: castle
  economy: economy
buildings:
  maximumEconomyCount: 2
combat:
  minimumDamage: 1
  defendAttackPercent: 80
  defendDamageTakenPercent: 70
  lootPercent: 50
```

Library IDs and versions use letters, digits, dashes, or underscores. The content
folder must be beside the configured ruleset folder. Docker images include it at
`/app/content`. Missing files, version mismatches, unknown/duplicate selections,
invalid definitions, and incomplete unit knowledge fail catalog startup. Library
references cannot be mixed with inline unit, upgrade, knowledge, or building-stat
definitions. Inline rulesets remain supported for older fixtures and authoring.

Library `units` and `upgrades` use the same fields as resolved rulesets. A building
entry has `id`, `kind`, `health`, `defense`, `vision`, `income`, and `cost`. Library
`knowledge` contains `mechanics` and one card for every library unit when present.
Cards are filtered to the selected roster. Numerical unit facts always come from
the selected definitions. All library entries are validated, including unused ones.

The shipped `ranged-skirmish-v1` scenario demonstrates a restricted roster:
Vanguard and Ranger, Plating only, and no economy construction. It shares the
original ruleset and map layout; the original scenario retains its full roster.

## Scenario restrictions

Add these optional fields to a normal scenario file (alongside its map, timing,
fallback, and scripted plans):

```yaml
rulesetId: example-v1
allowedUnits: [ranger]
allowedUpgrades: []
allowedBuildings: [castle]
```

An omitted/null list inherits the ruleset choices. An explicit empty upgrade list
allows no upgrades. At least one unit and the selected castle building are required;
empty unit/building lists are rejected. A scenario can only narrow its ruleset.
Excluding its economy building sets the maximum economy count to zero, removes
construction sites from AI legal choices, and disables default construction.
The map still uses the existing castle spawns and economy-site geometry.

Explicit scripted/fallback recruitment orders or priorities must use allowed
units. Explicit construction sites are rejected when economy buildings are disabled.
Generic scripted plans with unspecified recruitment use deterministic round-robin
purchases from the sorted legal unit roster, capped at six units and available gold.
Faction profiles have no ruleset binding. Scenario restrictions constrain both
opening and subsequent purchases, including upgrades. `startingGold` funds opening
units and upgrades; unspent gold carries forward. There is no design-point budget.

## Versioning and runtime behavior

When adding or balancing library content, publish a new library version and point
a new ruleset/scenario version at it. A library-backed ruleset carries source ID,
version, and a content digest. Battle creation resolves all definitions into the
existing scenario JSON snapshot; no runtime library lookup or database migration
is required. Library metadata and the resolved selection participate in the battle
snapshot hash. Editing files cannot change an existing battle's saved rules.

Both faction-visible AI context and knowledge retrieval use the resolved roster.
Engine and MCP purchase validation reject excluded units. The same rule applies
to explicit purchase orders and legacy recruitment priorities.

Run backend coverage and frontend coverage/build before deployment:

```sh
./scripts/test/test-go-coverage.sh
(cd frontend && npm test -- --coverage && npm run build)
./deploy-local.sh
```

Deploy content, catalog code, engine, and UI together. Use a new battle to test new
selections; old battles retain their saved definitions.
