package catalog

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/testutil"

	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"

	service "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func TestLegacyBattleTraceUnchanged(t *testing.T) {
	// These digests were captured from the pre-expansion HEAD in an isolated checkout.
	c, err := LoadCatalog("../../../scenarios", "../../../rulesets")
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	s, digest, err := c.Resolve("castle-siege-v1", [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	testutil.Require(t, !(digest != "d757206a477a8ce8870cf4b732a0e684c464643f4c23fef5cf10837d61628170" || s.Ruleset.ContentSource.Digest != "49b6d45f8a78b78b61af307f758ea68919ecfd1111fadf99a64956bc7b464121"), func() { t.Fatal("legacy content/snapshot hash changed") })
	m, err := game.NewMatch("compat", s, 200)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	h := sha256.New()
	for !m.Finished() {
		transition, err := m.Advance()
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
		data, err := json.Marshal(transition)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
		h.Write(data)
	}
	testutil.Require(t, hex.EncodeToString(h.Sum(nil)) == "18920e9cab1f8cc2b101dd3a13f336591bf16321ffe2cef470c6d945ef69d71a", func() { t.Fatal("legacy battle transition trace changed") })
}

func TestFrontierCatalogMapAndDeterministicBattle(t *testing.T) {
	c, err := LoadCatalog("../../../scenarios", "../../../rulesets")
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	s, _, err := c.Resolve("frontier-siege-v2", [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	testutil.Require(t, !(s.Map.Width != 25 || s.Map.Height != 17 || len(s.Map.EconomySites[0]) != 6 || len(s.Ruleset.Units) != 7 || len(s.Ruleset.Structures) != 3), func() { t.Fatal("incomplete frontier content") })
	testutil.Require(t, s.Ruleset.ContentSource.Version == "2", func() { t.Fatal("incorrect content version") })
	checkFrontierRoutes(t, s.Map)
	first, err := game.NewMatch("replay", s, 100)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	second, _ := game.NewMatch("replay", s, 100)
	kinds := map[string]bool{}
	forward := false
	for !first.Finished() {
		a, err := first.Advance()
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
		b, err := second.Advance()
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
		testutil.Require(t, reflect.DeepEqual(a, b), func() { t.Fatalf("nondeterministic tick %d", a.Tick) })
		for _, event := range a.Events {
			kinds[event.Type] = true
			if event.Type == "unit_recruited" {
				if id, _ := event.Data["sourceBuildingId"].(string); id != "a:castle" && id != "b:castle" && id != "" {
					forward = true
				}
			}
		}
	}
	testutil.Require(t, !(!kinds["building_constructed"] || !kinds["building_attacked"] || !kinds["unit_healed"] || !forward), func() {
		t.Fatalf("combined-arms scenario not exercising features: events %v, forward %v", kinds, forward)
	})
	// Serialized scenarios retain their own capabilities even when catalog copies change.
	data, _ := json.Marshal(s)
	var snapshot game.Scenario
	{
		err := json.Unmarshal(data, &snapshot)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	copy, _ := c.Ruleset("castle-siege-v2")
	copy.Structures[0].Attack = 999
	original, _ := c.Ruleset("castle-siege-v2")
	testutil.Require(t, !(original.Structures[0].Attack == 999 || snapshot.Ruleset.Structures[0].Attack != 12), func() { t.Fatal("catalog aliasing") })
	legacy, _ := c.Ruleset("castle-siege-v1")
	testutil.Require(t, !(len(legacy.Units) != 3 || len(legacy.Structures) != 0 || legacy.Units[0].Class != ""), func() { t.Fatal("legacy roster altered") })
}

func TestExpansionScenarioRestrictions(t *testing.T) {
	c, err := LoadCatalog("../../../scenarios", "../../../rulesets")
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	r, _ := c.Ruleset("castle-siege-v2")
	restricted, err := restrictRuleset(r, template{AllowedBuildings: []string{"castle", "defense_tower"}})
	testutil.Require(t, !(err != nil || len(restricted.Structures) != 1 || restricted.Structures[0].ID != "defense_tower" || restricted.Buildings.MaximumEconomyCount != 0), func() { t.Fatalf("restriction: %+v %v", restricted, err) })
	{
		_, err = restrictRuleset(r, template{AllowedBuildings: []string{"watchtower"}})
		testutil.Require(t, err != nil, func() { t.Fatal("castle exclusion allowed") })
	}
	r.Structures = append(r.Structures, r.Structures[0])
	testutil.Require(t, game.ValidateRuleset(r) != nil, func() { t.Fatal("duplicate structure accepted") })
}

func checkFrontierRoutes(t *testing.T, gameMap game.Map) {
	for _, sites := range gameMap.EconomySites {
		for _, site := range sites {
			free := 0
			for _, p := range game.Neighbors(site) {
				if gameMap.Traversable(p) {
					free++
				}
			}
			testutil.Require(t, free >= 4, func() { t.Fatalf("site %v lacks siege/recruitment access", site) })
		}
	}
	for _, y := range []int{0, 8, 16} {
		for x := 0; x < gameMap.Width; x++ {
			testutil.Require(t, gameMap.Traversable(game.Position{X: x, Y: y}), func() { t.Fatal("central/flanking route obstructed") })
		}
	}

}
