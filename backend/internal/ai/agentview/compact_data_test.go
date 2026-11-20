package agentview

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
)

// Independent expansion of the documented wire format, used only by tests.
func expandTable(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	object, ok := jsonObject(raw)
	if !ok {
		return raw
	}
	if groups, exists := object["groups"]; exists {
		return expandGroups(t, groups)
	}
	var table recordTable
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]json.RawMessage{}
	for _, values := range table.Rows {
		row := contextObject(table.Shared)
		if row == nil {
			row = map[string]json.RawMessage{}
		}
		for i, value := range values {
			if string(value) != "null" {
				row[table.Fields[i]] = value
			}
		}
		rows = append(rows, row)
	}
	return contextJSON(rows)
}
func expandGroups(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var tables []json.RawMessage
	_ = json.Unmarshal(raw, &tables)
	rows := []json.RawMessage{}
	for _, table := range tables {
		var part []json.RawMessage
		_ = json.Unmarshal(expandTable(t, table), &part)
		rows = append(rows, part...)
	}
	return contextJSON(rows)
}
func expandData(t *testing.T, compact map[string]json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	result := contextObject(compact)
	if string(result["encoding"]) != `"tables-v1"` {
		return result
	}
	delete(result, "encoding")
	expandLegal(t, result)
	expandUnits(t, result)
	expandMemory(result)
	return result
}
func expandLegal(t *testing.T, result map[string]json.RawMessage) {
	t.Helper()
	legal, ok := jsonObject(result["legal"])
	if !ok {
		return
	}
	for _, key := range []string{"units", "buildings", "upgrades"} {
		if value, exists := legal[key]; exists {
			legal[key] = expandTable(t, value)
		}
	}
	if raw, exists := legal["constructionRuns"]; exists {
		legal["economySites"] = expandRuns(raw)
		delete(legal, "constructionRuns")
	}
	result["legal"] = contextJSON(legal)
}
func expandRuns(raw json.RawMessage) json.RawMessage {
	var runs [][3]int64
	_ = json.Unmarshal(raw, &runs)
	sites := []map[string]int64{}
	for _, run := range runs {
		for x := run[1]; ; x++ {
			sites = append(sites, map[string]int64{"x": x, "y": run[0]})
			if x == run[2] {
				break
			}
		}
	}
	return contextJSON(sites)
}
func expandUnits(t *testing.T, result map[string]json.RawMessage) {
	t.Helper()
	view, ok := jsonObject(result["view"])
	if !ok || view["unitMap"] == nil {
		return
	}
	var units []map[string]json.RawMessage
	_ = json.Unmarshal(expandTable(t, view["unitMap"]), &units)
	state, _ := jsonObject(view["state"])
	var factions []map[string]json.RawMessage
	_ = json.Unmarshal(state["factions"], &factions)
	grouped := map[int][]map[string]json.RawMessage{}
	for _, unit := range units {
		var index int
		var xy [2]int64
		_ = json.Unmarshal(unit["faction"], &index)
		_ = json.Unmarshal(unit["xy"], &xy)
		delete(unit, "faction")
		delete(unit, "xy")
		unit["position"] = contextJSON(map[string]int64{"x": xy[0], "y": xy[1]})
		grouped[index] = append(grouped[index], unit)
	}
	for i, rows := range grouped {
		factions[i]["units"] = contextJSON(rows)
	}
	state["factions"] = contextJSON(factions)
	view["state"] = contextJSON(state)
	delete(view, "unitMap")
	result["view"] = contextJSON(view)
}
func expandMemory(result map[string]json.RawMessage) {
	var entries []map[string]json.RawMessage
	if json.Unmarshal(result["memory"], &entries) != nil || len(entries) == 0 {
		return
	}
	shared, hasShared := jsonObject(result["memoryEconomyShared"])
	active, _ := jsonObject(result["activePlan"])
	for _, entry := range entries {
		expandMemoryEntry(entry, active, shared, hasShared)
	}
	result["memory"] = contextJSON(entries)
	delete(result, "memoryEconomyShared")
}
func expandMemoryEntry(entry, active, shared map[string]json.RawMessage, hasShared bool) {
	var fields []string
	if json.Unmarshal(entry["planFieldsFromActive"], &fields) == nil {
		plan, _ := jsonObject(entry["plan"])
		for _, field := range fields {
			plan[field] = active[field]
		}
		entry["plan"] = contextJSON(plan)
		delete(entry, "planFieldsFromActive")
	}
	if hasShared {
		economy, _ := jsonObject(entry["economy"])
		for key, value := range shared {
			economy[key] = value
		}
		entry["economy"] = contextJSON(economy)
	}
}
func assertRoundTrip(t *testing.T, source map[string]json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	before := contextJSON(source)
	compact := compactDecisionData(source)
	if !sameContextJSON(contextJSON(expandData(t, compact)), before) {
		t.Fatalf("lost values:\n%s\n%s", before, contextJSON(compact))
	}
	if string(contextJSON(source)) != string(before) {
		t.Fatal("source mutated")
	}
	if len(contextJSON(compact)) > len(before) {
		t.Fatal("encoding grew")
	}
	if string(contextJSON(compactDecisionData(source))) != string(contextJSON(compact)) {
		t.Fatal("nondeterministic encoding")
	}
	return compact
}
func TestCompactDataPreservesArmies(t *testing.T) {
	for _, count := range []int{0, 1, 46, 200} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			_, c := compactFixture()
			c.View = compressionView(count)
			source := groupContextFactions(deduplicateContext(battlefieldContext{Context: Compact(c), View: compactBattlefieldView(c.View)}))
			compact := assertRoundTrip(t, source)
			if count >= 46 && contextObject(compact["view"])["unitMap"] == nil {
				t.Fatal("army not consolidated")
			}
		})
	}
}
func TestTablesPreserveValues(t *testing.T) {
	for _, fixture := range []string{`null`, `[]`, `[null]`, `[{"x":null}]`, `[{}]`, `[{"x":1}]`,
		`[{"large":9007199254740993,"same":"repeat this value","unknown":{"nested":true}},{"large":9223372036854775807,"same":"repeat this value","unknown":[]},{"same":"repeat this value"}]`,
		`[{"id":"a","track":"attack","level":1,"attackBonus":0},{"id":"b","track":"attack","level":2},{"id":"c","exclusiveGroup":"choice"},{"id":"d","exclusiveGroup":"choice","attackBonus":2}]`} {
		raw := json.RawMessage(fixture)
		for _, encoded := range []json.RawMessage{compactTable(raw), compactUpgrades(raw)} {
			if !sameContextJSON(expandTable(t, encoded), raw) || len(encoded) > len(raw) {
				t.Fatalf("not lossless: %s", fixture)
			}
		}
	}
}
func TestSiteRunsKeepGapsAndOrder(t *testing.T) {
	source := contextObject(json.RawMessage(`{"legal":{"economySites":[{"x":2,"y":1},{"x":3,"y":1},{"x":3,"y":1},{"x":8,"y":1},{"x":-1,"y":0},{"x":0,"y":0},{"x":9223372036854775807,"y":0}]},"future":{"n":9007199254740993}}`))
	compact := assertRoundTrip(t, source)
	if contextObject(compact["legal"])["constructionRuns"] == nil {
		t.Fatal("sites not compacted")
	}
}
func TestMemoryKeepsHistoricalDifferences(t *testing.T) {
	source := contextObject(json.RawMessage(`{"activePlan":{"name":"now","actionWeights":{"attack":1000,"move":500,"heal":800,"defend":200},"constructionPriorities":[{"x":1,"y":2},{"x":3,"y":4}]},"memory":[{"plan":{"name":"then","actionWeights":{"attack":1000,"move":500,"heal":800,"defend":200},"constructionPriorities":[{"x":1,"y":2},{"x":3,"y":4}]},"economy":{"gold":90,"maximumBuildings":20,"maximumEconomyBuildings":10}},{"plan":{"name":"before","actionWeights":{"attack":500}},"economy":{"gold":12,"maximumBuildings":20,"maximumEconomyBuildings":10}}]}`))
	compact := assertRoundTrip(t, source)
	if compact["memoryEconomyShared"] == nil {
		t.Fatal("shared economy not compacted")
	}
}
func TestUnsupportedCompactInputsStayInline(t *testing.T) {
	for _, fixture := range []string{
		`{}`, `{"encoding":"future"}`, `{"legal":null,"view":null,"memory":null}`, `{"view":{"state":null}}`, `{"view":{"state":{"factions":{}}}}`,
		`{"view":{"state":{"factions":[{"units":[]}]}}}`, `{"view":{"state":{"factions":[{"units":[{"xy":[1,2]}]}]}}}`,
		`{"legal":{"constructionRuns":[],"economySites":[{"x":1,"y":2}]}}`, `{"legal":{"economySites":[{"x":null,"y":2}]}}`,
		`{"legal":{"economySites":[{"x":1.5,"y":2}]}}`, `{"legal":{"economySites":[{"x":1,"y":2,"future":true}]}}`,
		`{"memory":[{"plan":{},"economy":null}]}`, `{"memory":[{"plan":{"x":1}}]}`, `{"memoryEconomyShared":{},"memory":[]}`,
		`{"memory":[{"planFieldsFromActive":[],"plan":{"x":1},"economy":{}}]}`,
	} {
		assertRoundTrip(t, contextObject(json.RawMessage(fixture)))
	}
}
func TestCompactedStateDoesNotPolluteReference(t *testing.T) {
	_, c := compactFixture()
	c.View = compressionView(46)
	first := contextJSON(map[string]any{"context": Observation(c)})
	c.View.State.Units[0].Position.X++
	c.RequestID = "next"
	c.TargetTick++
	second := contextJSON(map[string]any{"context": Observation(c)})
	r1, s1, _ := ContextBlocks(first)
	r2, s2, _ := ContextBlocks(second)
	if string(r1) != string(r2) || sameContextJSON(s1, s2) {
		t.Fatal("unit movement changed static reference or disappeared")
	}
}
func TestCompactRecordedDecisions(t *testing.T) {
	path := os.Getenv("BATTLEFIELD_DECISIONS_FIXTURE")
	if path == "" {
		t.Skip("optional private offline replay")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Observation ai.DecisionContext `json:"observation"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		source := groupContextFactions(deduplicateContext(battlefieldContext{Context: Compact(row.Observation), View: compactBattlefieldView(row.Observation.View)}))
		compact := assertRoundTrip(t, source)
		t.Logf("decision %d: %d -> %d bytes", i, len(contextJSON(source)), len(contextJSON(compact)))
	}
}

func TestUpgradeTracksUseGroupedTables(t *testing.T) {
	upgrades := []map[string]any{}
	for _, track := range []string{"attack", "armor", "industry"} {
		for level := 1; level <= 6; level++ {
			upgrades = append(upgrades, map[string]any{"id": fmt.Sprintf("%s-%d", track, level), "track": track, "level": level, "cost": level * 10, "description": "Shared explanation of this upgrade track: " + track, "targetClass": "infantry", "attackBonus": level})
		}
	}
	raw := contextJSON(upgrades)
	compact := compactUpgrades(raw)
	if contextObject(compact)["groups"] == nil || !sameContextJSON(expandTable(t, compact), raw) {
		t.Fatal("upgrade tracks did not round trip through grouped arrays")
	}
}

func TestReservedNamesNeverBecomeEncodingInstructions(t *testing.T) {
	_, c := compactFixture()
	source := groupContextFactions(deduplicateContext(battlefieldContext{Context: Compact(c), View: compactBattlefieldView(c.View)}))
	source["memoryEconomyShared"] = contextJSON(map[string]any{"future": true})
	before := contextJSON(source)
	if !sameContextJSON(contextJSON(compactDecisionData(source)), before) {
		t.Fatal("future reserved field reinterpreted")
	}
}

func TestInvalidUnitPositionsRemainInline(t *testing.T) {
	for _, unit := range []string{`{"id":"x"}`, `{"position":null}`, `{"position":{"x":null,"y":2}}`, `{"position":{"x":1,"y":2},"faction":0}`, `{"position":{"x":1,"y":2},"xy":[]}`} {
		source := contextObject(json.RawMessage(`{"view":{"state":{"factions":[{"units":[` + unit + `]}]}}}`))
		assertRoundTrip(t, source)
	}
}
