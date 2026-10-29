// Checks legacy inputs load under the current rules and the frontier content is
// complete, deterministic and independent of catalog copies.

package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/gamedata"
	service "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func TestLegacyInputWithOpenMapRules(t *testing.T) {
	// Content/snapshot digests include the shortened authored guidance. The
	// simulation digest below explicitly reflects the open-map, queue and
	// ally pass-through movement rules.
	s := legacyScenario(t, "castle-siege-v1")
	digest := digestOf(t, s)
	if digest != "356a05c48256e0840499b2969648215e58726e7bbd48966605288ea90aa8fd24" || s.Ruleset.ContentSource.Digest != "2f1d5db379cb23dc35af710547520401c03fa5a36242237146df58b40948f6ad" {
		t.Fatal("content/snapshot hash changed")
	}
	m, err := game.NewMatch("compat", s, 200)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for !m.Finished() {
		transition, err := m.Advance()
		if err != nil {
			t.Fatal(err)
		}
		withoutVisualizationMetadata(&transition)
		data, err := json.Marshal(transition)
		if err != nil {
			t.Fatal(err)
		}
		h.Write(data)
	}
	if trace := hex.EncodeToString(h.Sum(nil)); trace != "58caa9fe48fddbf0c9b6515c97bc7acff79f7b40ebda35a166bb12e66dfa3d84" {
		t.Fatalf("open-map transition trace changed to %s (legacy pads remain impassable)", trace)
	}
}

// withoutVisualizationMetadata clears the attack and route visualization fields and the
// per-type statistics added after the legacy input format; gameplay fields remain part
// of the trace.
func withoutVisualizationMetadata(transition *game.Transition) {
	transition.State.UnitDeaths = nil
	for i := range transition.State.Units {
		transition.State.Units[i].MovementPath = nil
		transition.State.Units[i].AttackOutcome = ""
	}
	for i := range transition.State.Buildings {
		transition.State.Buildings[i].AttackOutcome = ""
	}
	for _, event := range transition.Events {
		delete(event.Data, "attackMode")
		delete(event.Data, "outcome")
		delete(event.Data, "impactPosition")
		for _, key := range []string{"unitType", "killerId", "killerType", "killerFactionId"} {
			if event.Type == "unit_destroyed" || event.Type == "building_destroyed" || event.Type == "castle_destroyed" {
				delete(event.Data, key)
			}
		}
	}
	if transition.Result != nil {
		for i := range transition.Result.Factions {
			faction := &transition.Result.Factions[i]
			faction.UnitTypes, faction.BuildingTypes, faction.Upgrades = nil, nil, nil
		}
	}
}

func TestFrontierCatalogMapAndDeterministicBattle(t *testing.T) {
	skipSequentialBattleUnderRace(t)
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Resolve("frontier-siege", [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Map.Width != 25 || s.Map.Height != 17 || len(s.Map.EconomySites[0]) != 0 || len(s.Ruleset.Units) != 14 || len(s.Ruleset.Structures) != 7 {
		t.Fatal("incomplete frontier content")
	}
	if s.Ruleset.ContentSource.Version != "10" {
		t.Fatal("incorrect content version")
	}
	checkFrontierRoutes(t, s.Map)
	kinds, forward := replayFrontierTwice(t, s)
	if !kinds["building_constructed"] || !kinds["building_attacked"] || !kinds["unit_healed"] || !forward {
		t.Fatalf("combined-arms scenario not exercising features: events %v, forward %v", kinds, forward)
	}
	checkCatalogCopiesAreIndependent(t, c, s)
}

// replayFrontierTwice runs two matches of the scenario in lockstep, requiring identical
// transitions, and reports the event types seen and whether any unit was recruited
// from a forward building rather than a castle.
func replayFrontierTwice(t *testing.T, s game.Scenario) (map[string]bool, bool) {
	t.Helper()
	first, err := game.NewMatch("replay", s, 200)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := game.NewMatch("replay", s, 200)
	kinds := map[string]bool{}
	forward := false
	for !first.Finished() {
		a, err := first.Advance()
		if err != nil {
			t.Fatal(err)
		}
		b, err := second.Advance()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("nondeterministic tick %d", a.Tick)
		}
		for _, event := range a.Events {
			kinds[event.Type] = true
			forward = forward || recruitedForward(event)
		}
	}
	return kinds, forward
}

func recruitedForward(event game.Event) bool {
	if event.Type != "unit_recruited" {
		return false
	}
	id, _ := event.Data["sourceBuildingId"].(string)
	return id != "a:castle" && id != "b:castle" && id != ""
}

// checkCatalogCopiesAreIndependent requires serialized scenarios and catalog rulesets to
// keep their own capabilities when a returned copy is changed.
func checkCatalogCopiesAreIndependent(t *testing.T, c *Catalog, s game.Scenario) {
	t.Helper()
	data, _ := json.Marshal(s)
	var snapshot game.Scenario
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	copy, _ := c.Ruleset("battlefield")
	copy.Structures[0].Attack = 999
	copy.Buildings.CastleTrains[0] = "changed"
	original, _ := c.Ruleset("battlefield")
	if original.Structures[0].Attack == 999 || snapshot.Ruleset.Structures[0].Attack != 15 || original.Buildings.CastleTrains[0] != "vanguard" {
		t.Fatal("catalog aliasing")
	}
}

func checkFrontierRoutes(t *testing.T, gameMap game.Map) {
	t.Helper()
	for _, sites := range gameMap.EconomySites {
		for _, site := range sites {
			if traversableNeighbors(gameMap, site) < 4 {
				t.Fatalf("site %v lacks siege/recruitment access", site)
			}
		}
	}
	for _, y := range []int{0, 8, 16} {
		for x := 0; x < gameMap.Width; x++ {
			if !gameMap.Traversable(game.Position{X: x, Y: y}) {
				t.Fatal("central/flanking route obstructed")
			}
		}
	}
}

func traversableNeighbors(gameMap game.Map, site game.Position) int {
	free := 0
	for _, p := range game.Neighbors(site) {
		if gameMap.Traversable(p) {
			free++
		}
	}
	return free
}

func TestOpenMapContentIncludesTerrainRangesAndTraining(t *testing.T) {
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := c.Ruleset("battlefield")
	checkTrainingCatalog(t, rules)
	checkConstructionCatalog(t, rules)
	for _, id := range c.IDs() {
		scenario, err := c.Resolve(id, [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
		if err != nil {
			t.Fatal(err)
		}
		checkSpecialTerrain(t, id, scenario.Map)
	}
}

func checkTrainingCatalog(t *testing.T, rules game.Ruleset) {
	t.Helper()
	times := map[uint64]bool{}
	for _, u := range rules.Units {
		if u.TrainingTicks == 0 || u.Vision != 0 {
			t.Fatalf("current unit lacks training or retains vision: %+v", u)
		}
		times[u.TrainingTicks] = true
	}
	if len(times) < 3 {
		t.Fatal("roster lacks distinct balancing times")
	}
}

func checkConstructionCatalog(t *testing.T, rules game.Ruleset) {
	t.Helper()
	if rules.Buildings.CastleBuildRange != 4 || rules.Buildings.EconomyBuildRange != 3 {
		t.Fatal("wrong core build ranges")
	}
	outpost := false
	for _, b := range rules.Structures {
		if b.Kind == game.BuildingWatchtower || b.Vision != 0 {
			t.Fatal("vision-only building remains")
		}
		if b.Kind == game.BuildingOutpost {
			outpost = b.BuildRange == 5
		}
	}
	if !outpost {
		t.Fatal("outpost missing")
	}
}

func checkSpecialTerrain(t *testing.T, id string, gameMap game.Map) {
	t.Helper()
	terrain := map[string]int{}
	for y := 0; y < gameMap.Height; y++ {
		for x := 0; x < gameMap.Width; x++ {
			p := game.Position{X: x, Y: y}
			kind := gameMap.TerrainAt(p)
			terrain[kind]++
			if kind == game.TerrainGold && traversableNeighbors(gameMap, p) < 4 {
				t.Fatalf("inaccessible deposit: %s %v", id, p)
			}
		}
	}
	if terrain[game.TerrainForest] == 0 || terrain[game.TerrainGold] == 0 || terrain[game.TerrainBuildSite] != 0 {
		t.Fatalf("special terrain missing or pads remain: %s %v", id, terrain)
	}
}

func TestSpecializedCatalogRosters(t *testing.T) {
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := c.Ruleset("battlefield")
	expected := map[string][]string{
		game.BuildingBarracks:      {"vanguard", "striker", "pikeman"},
		game.BuildingArcheryRange:  {"ranger", "crossbowman", "gunner"},
		game.BuildingStables:       {"scout", "raider_cavalry", "heavy_cavalry"},
		game.BuildingSiegeWorkshop: {"catapult", "battering_ram", "engineer", "cannon"},
		game.BuildingInfirmary:     {"herbalist"},
	}
	for _, b := range rules.Structures {
		if roster, ok := expected[b.Kind]; ok {
			if !reflect.DeepEqual(b.Trains, roster) {
				t.Fatalf("%s roster: %v", b.Kind, b.Trains)
			}
			delete(expected, b.Kind)
		}
	}
	if len(expected) != 0 {
		t.Fatal("missing production buildings", expected)
	}
	// A returned catalog roster must not mutate another resolved scenario.
	rules.Structures[2].Trains[0] = "changed"
	fresh, _ := c.Ruleset("battlefield")
	if fresh.Structures[2].Trains[0] != "vanguard" {
		t.Fatal("roster aliases catalog")
	}
}

func TestSpecializedCatalogStartingBuildings(t *testing.T) {
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	for _, mapID := range c.IDs() {
		scenario, err := c.Resolve(mapID, [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
		if err != nil {
			t.Fatal(err)
		}
		m, err := game.NewMatch("opening", scenario, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(m.State().Buildings) != 2 {
			t.Fatal("expected only a castle per faction")
		}
		for _, faction := range m.FactionIDs() {
			decision, err := m.DecisionSnapshot(faction)
			if err != nil || len(decision.RecruitmentSources) != 1 || decision.RecruitmentSources[0] != faction+":castle" {
				t.Fatal("starting source", decision, err)
			}
		}
	}
}

func TestTowerDurabilityPerGold(t *testing.T) {
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := c.Ruleset("battlefield")
	var tower game.BuildingType
	for _, b := range rules.Structures {
		if b.Kind == game.BuildingTower {
			tower = b
		}
	}
	if tower.Cost != 60 || tower.Attack != 15 || tower.Health != 130 || tower.Range != 3 {
		t.Fatal("tower balance", tower)
	}
	for _, u := range rules.Units {
		if tower.Health*u.Cost <= u.Health*tower.Cost {
			t.Fatalf("tower durability per gold below %s", u.ID)
		}
	}
}
