package agentview

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/briefing"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/decision"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func compactFixture() (game.Ruleset, ai.DecisionContext) {
	r, c := knowledgeFixture()
	c.SchemaVersion = ai.ContextSchema
	c.Legal.Units = append([]game.UnitType(nil), r.Units[:8]...)
	c.Knowledge, _ = briefing.RetrieveKnowledge(r, c, ai.DefaultKnowledgeLimits())
	c.UnitReference = FreezeUnitReference(r, c)
	return r, c
}
func TestCompactProjectionAndFrozenLookup(t *testing.T) {
	r, c := compactFixture()
	initial := Compact(c)
	if len(initial.Legal.Units) != 8 || len(initial.ReferenceUnits) != 2 || !reflect.DeepEqual(initial.View, c.View) {
		t.Fatal("lost legal/visible observations")
	}
	payload, _ := json.Marshal(Observation(c))
	for _, forbidden := range []string{`"unitReference"`, `"stats"`, `"guidance"`, `"cards"`} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("leaked %s", forbidden)
		}
	}
	before, _ := decision.ObservationHash(c)
	r.Units[0].Attack = 999
	r.Knowledge.Cards[0].Guidance = "edited"
	result, err := LookupUnitDetailsPage(c, []string{"unit-00", "unit-20", "unit-23", "unknown"}, 0)
	if err != nil || !result.Units[0].Available || result.Units[0].Stats.Attack == 999 || result.Units[0].Guidance == "edited" || !result.Units[1].Available || result.Units[2].Available || result.Units[3].Available {
		t.Fatalf("bad frozen lookup: %+v %v", result, err)
	}
	if result.Digest != c.UnitReference.Digest {
		t.Fatal("wrong identity")
	}
	c.UnitReference.Units[0].Stats.Attack++
	after, _ := decision.ObservationHash(c)
	if before == after {
		t.Fatal("reference not hashed")
	}
	// Even an accidentally overbroad stored snapshot cannot expose a hidden type.
	c.UnitReference.Units = append(c.UnitReference.Units, ai.UnitDetail{UnitID: "hidden", Stats: game.UnitType{ID: "hidden"}})
	hidden, _ := LookupUnitDetailsPage(c, []string{"hidden"}, 0)
	if hidden.Units[0].Available {
		t.Fatal("hidden reference exposed")
	}
}
func TestUnitDetailsMissingKnowledgeAndInvalidBatches(t *testing.T) {
	r, c := compactFixture()
	r.Knowledge = nil
	c.Knowledge = nil
	c.UnitReference = FreezeUnitReference(r, c)
	got, err := LookupUnitDetailsPage(c, []string{"unit-00"}, 0)
	if err != nil || !got.Units[0].Available || got.Units[0].Guidance != "" {
		t.Fatal(got, err)
	}
	overview := Compact(c)
	if overview.Legal.Units[0].Role != "" {
		t.Fatal("invented role")
	}
	for _, ids := range [][]string{nil, {}, {""}, {"unit-00", "unit-00"}, {"a", "b", "c", "d", "e"}} {
		if _, err := LookupUnitDetailsPage(c, ids, 0); err == nil {
			t.Fatalf("accepted %v", ids)
		}
	}
}
func TestLegacyContextAndLookup(t *testing.T) {
	_, c := compactFixture()
	c.SchemaVersion = "battlefield.decision-context.v2"
	c.UnitReference = nil
	details, err := LookupUnitDetailsPage(c, []string{"unit-00", "unit-20", "unit-23"}, 0)
	if err != nil || !details.Units[0].Available || !details.Units[1].Available || details.Units[2].Available {
		t.Fatal(details, err)
	}
	c.Knowledge = nil
	details, err = LookupUnitDetailsPage(c, []string{"unit-00", "unit-20"}, 0)
	if err != nil || !details.Units[0].Available || details.Units[1].Available {
		t.Fatal(details, err)
	}
	c.SchemaVersion = ai.ContextSchema
	compact := Compact(c)
	if compact.Knowledge != nil {
		t.Fatal("invented knowledge")
	}
	_, withKnowledge := compactFixture()
	withKnowledge.UnitReference = nil
	if Compact(withKnowledge).Knowledge.Mechanics == "" {
		t.Fatal("lost mechanics")
	}
}
func TestCompactContextSizeByBattleStage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		units int
	}{{"opening", 0}, {"midgame", 24}, {"large", 200}} {
		t.Run(tc.name, func(t *testing.T) {
			r, c := compactFixture()
			c.Opening = tc.units == 0
			c.View.State.Units = nil
			for i := 0; i < tc.units; i++ {
				c.View.State.Units = append(c.View.State.Units, game.Unit{ID: fmt.Sprint(i), Type: "unit-00", Health: 50, MaxHealth: 100, Attack: 20, Defense: 10})
			}
			c.UnitReference = FreezeUnitReference(r, c)
			c.Knowledge, _ = briefing.RetrieveKnowledge(r, c, ai.DefaultKnowledgeLimits())
			compact := Compact(c)
			full := c
			full.UnitReference = nil
			full.SchemaVersion = "battlefield.decision-context.v2"
			fullJSON, _ := json.Marshal(full)
			compactJSON, _ := json.Marshal(compact)
			catalogBefore, _ := json.Marshal(struct {
				Legal     ai.LegalChoices
				Knowledge *ai.DecisionKnowledge
			}{c.Legal, c.Knowledge})
			catalogAfter, _ := json.Marshal(struct {
				Legal          LegalChoices
				Knowledge      *Knowledge
				ReferenceUnits []UnitOverview
			}{compact.Legal, compact.Knowledge, compact.ReferenceUnits})
			if len(catalogAfter) >= len(catalogBefore) || len(compactJSON) > len(fullJSON) {
				t.Fatalf("no reduction: catalog %d -> %d; total %d -> %d", len(catalogBefore), len(catalogAfter), len(fullJSON), len(compactJSON))
			}
			view, _ := json.Marshal(c.View)
			memory, _ := json.Marshal(c.Memory)
			economy, _ := json.Marshal(c.Economy)
			t.Logf("bytes: catalog %d -> %d; initial %d -> %d; view %d; memory %d; economy %d", len(catalogBefore), len(catalogAfter), len(fullJSON), len(compactJSON), len(view), len(memory), len(economy))
		})
	}
}
func TestFourSmallUnitDetailsFitInline(t *testing.T) {
	_, c := compactFixture()
	result, err := LookupUnitDetailsPage(c, []string{"unit-00", "unit-01", "unit-02", "unit-03"}, 0)
	payload, _ := json.Marshal(result)
	if err != nil || len(payload) > UnitDetailsMaxBytes || len(result.Units) != 4 {
		t.Fatal(len(payload), err)
	}
}

func TestOversizedDetailsAskForSmallerBatch(t *testing.T) {
	r, c := compactFixture()
	guidance := strings.Repeat("\x01é", 341)
	for i := range r.Knowledge.Cards {
		r.Knowledge.Cards[i].Guidance = guidance
	}
	c.UnitReference = FreezeUnitReference(r, c)
	if _, err := LookupUnitDetailsPage(c, []string{"unit-00", "unit-01", "unit-02", "unit-03"}, 0); err == nil {
		t.Fatal("unbounded response")
	}
	assertGuidancePagination(t, c, guidance)
	for _, tc := range []struct {
		ids    []string
		offset int
	}{{[]string{"unit-00"}, -1}, {[]string{"unit-00", "unit-01"}, 2}, {[]string{"unit-00"}, 99999}, {[]string{"unit-00"}, 2}} {
		if _, err := LookupUnitDetailsPage(c, tc.ids, tc.offset); err == nil {
			t.Fatal("invalid offset accepted")
		}
	}
	c.UnitReference.RulesetID = strings.Repeat("x", UnitDetailsMaxBytes)
	if _, err := LookupUnitDetailsPage(c, []string{"unit-00"}, 0); err == nil {
		t.Fatal("unbounded metadata")
	}
}

func TestCompactOverviewIgnoresAutomaticCardLimit(t *testing.T) {
	r, c := knowledgeFixture()
	c.SchemaVersion = ai.ContextSchema
	c.Knowledge, _ = briefing.RetrieveKnowledge(r, c, ai.KnowledgeLimits{MaxUnitCards: 1, MaxBytes: 2048})
	c.UnitReference = FreezeUnitReference(r, c)
	stored, _ := json.Marshal(c)
	var restored ai.DecisionContext
	if err := json.Unmarshal(stored, &restored); err != nil {
		t.Fatal(err)
	}
	overview := Compact(restored)
	if len(overview.Legal.Units) != len(r.Units) || len(restored.Knowledge.Cards) != 1 {
		t.Fatal("automatic card limit changed legal overview")
	}
	detail, err := LookupUnitDetailsPage(restored, []string{r.Units[len(r.Units)-1].ID}, 0)
	if err != nil || !detail.Units[0].Available || detail.Units[0].Guidance == "" {
		t.Fatal("persisted reference omitted non-selected card", err)
	}
}

func assertGuidancePagination(t *testing.T, c ai.DecisionContext, guidance string) {
	t.Helper()
	joined := ""
	offset := 0
	pages := 0
	for {
		result, err := LookupUnitDetailsPage(c, []string{"unit-00"}, offset)
		payload, _ := json.Marshal(result)
		if err != nil || len(payload) > UnitDetailsMaxBytes || !result.Units[0].Available || result.Units[0].Stats == nil {
			t.Fatal(result, err)
		}
		unit := result.Units[0]
		joined += unit.Guidance
		pages++
		if unit.NextGuidanceOffset == nil {
			break
		}
		if *unit.NextGuidanceOffset <= offset {
			t.Fatal("pagination made no progress")
		}
		offset = *unit.NextGuidanceOffset
	}
	if joined != guidance || pages < 2 {
		t.Fatal("lost guidance or continuation")
	}
}

func TestContinuousProductionSurvivesCompaction(t *testing.T) {
	_, c := compactFixture()
	c.ContinuousProduction = true
	c.View.State.Buildings = []game.Building{{ID: "a:castle", FactionID: "a", Type: "castle", Health: 100, Trains: []string{"vanguard"}, Production: &game.BuildingProduction{UnitType: "vanguard", Enabled: true, WaitingForGold: true}}}
	data, err := json.Marshal(Observation(c))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"continuousProduction":true`, `"production":{"unitType":"vanguard","enabled":true,"waitingForGold":true}`} {
		if !strings.Contains(string(data), field) {
			t.Fatal("production missing from compact observation", string(data))
		}
	}
}
