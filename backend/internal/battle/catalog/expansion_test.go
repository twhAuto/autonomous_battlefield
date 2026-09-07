package catalog

import (
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
	if err != nil {
		t.Fatal(err)
	}
	s, digest, err := c.Resolve("castle-siege-v1", [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
	if err != nil {
		t.Fatal(err)
	}
	if digest != "d757206a477a8ce8870cf4b732a0e684c464643f4c23fef5cf10837d61628170" || s.Ruleset.ContentSource.Digest != "49b6d45f8a78b78b61af307f758ea68919ecfd1111fadf99a64956bc7b464121" {
		t.Fatal("legacy content/snapshot hash changed")
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

func TestFrontierCatalogMapAndDeterministicBattle(t *testing.T) {
	c, err := LoadCatalog("../../../scenarios", "../../../rulesets")
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := c.Resolve("frontier-siege-v2", [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Map.Width != 25 || s.Map.Height != 17 || len(s.Map.EconomySites[0]) != 6 || len(s.Ruleset.Units) != 7 || len(s.Ruleset.Structures) != 3 {
		t.Fatal("incomplete frontier content")
	}
	if s.Ruleset.ContentSource.Version != "2" {
		t.Fatal("incorrect content version")
	}
	for _, sites := range s.Map.EconomySites {
		for _, site := range sites {
			free := 0
			for _, p := range game.Neighbors(site) {
				if s.Map.Traversable(p) {
					free++
				}
			}
			if free < 4 {
				t.Fatalf("site %v lacks siege/recruitment access", site)
			}
		}
	}
	for _, y := range []int{0, 8, 16} {
		for x := 0; x < s.Map.Width; x++ {
			if !s.Map.Traversable(game.Position{X: x, Y: y}) {
				t.Fatal("central/flanking route obstructed")
			}
		}
	}
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
			if event.Type == "unit_recruited" {
				if id, _ := event.Data["sourceBuildingId"].(string); id != "a:castle" && id != "b:castle" && id != "" {
					forward = true
				}
			}
		}
	}
	if !kinds["building_constructed"] || !kinds["building_attacked"] || !kinds["unit_healed"] || !forward {
		t.Fatalf("combined-arms scenario not exercising features: events %v, forward %v", kinds, forward)
	}
	// Serialized scenarios retain their own capabilities even when catalog copies change.
	data, _ := json.Marshal(s)
	var snapshot game.Scenario
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	copy, _ := c.Ruleset("castle-siege-v2")
	copy.Structures[0].Attack = 999
	original, _ := c.Ruleset("castle-siege-v2")
	if original.Structures[0].Attack == 999 || snapshot.Ruleset.Structures[0].Attack != 12 {
		t.Fatal("catalog aliasing")
	}
	legacy, _ := c.Ruleset("castle-siege-v1")
	if len(legacy.Units) != 3 || len(legacy.Structures) != 0 || legacy.Units[0].Class != "" {
		t.Fatal("legacy roster altered")
	}
}

func TestExpansionScenarioRestrictions(t *testing.T) {
	c, err := LoadCatalog("../../../scenarios", "../../../rulesets")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := c.Ruleset("castle-siege-v2")
	restricted, err := restrictRuleset(r, template{AllowedBuildings: []string{"castle", "defense_tower"}})
	if err != nil || len(restricted.Structures) != 1 || restricted.Structures[0].ID != "defense_tower" || restricted.Buildings.MaximumEconomyCount != 0 {
		t.Fatalf("restriction: %+v %v", restricted, err)
	}
	if _, err = restrictRuleset(r, template{AllowedBuildings: []string{"watchtower"}}); err == nil {
		t.Fatal("castle exclusion allowed")
	}
	r.Structures = append(r.Structures, r.Structures[0])
	if game.ValidateRuleset(r) == nil {
		t.Fatal("duplicate structure accepted")
	}
}
