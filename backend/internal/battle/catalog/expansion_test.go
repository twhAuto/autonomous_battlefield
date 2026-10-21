// Checks the legacy battle trace stays fixed and the frontier expansion content is
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

func TestLegacyBattleTraceUnchanged(t *testing.T) {
	// Content/snapshot digests include the shortened authored guidance. The
	// simulation transition digest below remains the pre-expansion baseline.
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
	if hex.EncodeToString(h.Sum(nil)) != "18920e9cab1f8cc2b101dd3a13f336591bf16321ffe2cef470c6d945ef69d71a" {
		t.Fatal("legacy battle transition trace changed")
	}
}

// withoutVisualizationMetadata clears the attack and route visualization fields added
// after the legacy digest, so it keeps comparing every original simulation field
// without replacing the baseline.
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
	}
}

func TestFrontierCatalogMapAndDeterministicBattle(t *testing.T) {
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Resolve("frontier-siege", [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Map.Width != 25 || s.Map.Height != 17 || len(s.Map.EconomySites[0]) != 6 || len(s.Ruleset.Units) != 11 || len(s.Ruleset.Structures) != 3 {
		t.Fatal("incomplete frontier content")
	}
	if s.Ruleset.ContentSource.Version != "3" {
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
	first, err := game.NewMatch("replay", s, 100)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := game.NewMatch("replay", s, 100)
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
	original, _ := c.Ruleset("battlefield")
	if original.Structures[0].Attack == 999 || snapshot.Ruleset.Structures[0].Attack != 12 {
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
