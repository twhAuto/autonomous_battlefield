package agentview

import (
	"bytes"
	"encoding/json"
	"sort"
)

// Null table cells mean absent. Records containing explicit null stay inline.
type recordTable struct {
	Shared map[string]json.RawMessage `json:"shared,omitempty"`
	Fields []string                   `json:"fields"`
	Rows   [][]json.RawMessage        `json:"rows"`
}

func records(raw json.RawMessage) ([]map[string]json.RawMessage, bool) {
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 {
		return nil, false
	}
	for _, row := range rows {
		if row == nil {
			return nil, false
		}
		for _, value := range row {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return nil, false
			}
		}
	}
	return rows, true
}

func sharedValue(rows []map[string]json.RawMessage, key string, value json.RawMessage) bool {
	for _, row := range rows {
		if !sameContextJSON(row[key], value) {
			return false
		}
	}
	return true
}

func sharedFields(rows []map[string]json.RawMessage) map[string]json.RawMessage {
	shared := map[string]json.RawMessage{}
	for key, value := range rows[0] {
		if sharedValue(rows[1:], key, value) {
			shared[key] = value
		}
	}
	return shared
}

func tableFields(rows []map[string]json.RawMessage, shared map[string]json.RawMessage) []string {
	names := map[string]bool{}
	for _, row := range rows {
		for key := range row {
			if _, ok := shared[key]; !ok {
				names[key] = true
			}
		}
	}
	fields := make([]string, 0, len(names))
	for key := range names {
		fields = append(fields, key)
	}
	sort.Strings(fields)
	return fields
}

func tableRow(row map[string]json.RawMessage, fields []string) []json.RawMessage {
	values := make([]json.RawMessage, len(fields))
	end := 0
	for i, key := range fields {
		values[i] = row[key]
		if values[i] != nil {
			end = i + 1
		}
	}
	return values[:end]
}

func tableFor(rows []map[string]json.RawMessage) recordTable {
	shared := sharedFields(rows)
	fields := tableFields(rows, shared)
	table := recordTable{Shared: shared, Fields: fields, Rows: make([][]json.RawMessage, 0, len(rows))}
	for _, row := range rows {
		table.Rows = append(table.Rows, tableRow(row, fields))
	}
	return table
}

func compactTable(raw json.RawMessage) json.RawMessage {
	rows, ok := records(raw)
	if !ok {
		return raw
	}
	candidate := contextJSON(tableFor(rows))
	if len(candidate) < len(raw) {
		return candidate
	}
	return raw
}

// Preserve catalog order and missing bonuses rather than assuming zero defaults.
func compactUpgrades(raw json.RawMessage) json.RawMessage {
	rows, ok := records(raw)
	if !ok {
		return raw
	}
	groups := []recordTable{}
	start := 0
	for i := 1; i <= len(rows); i++ {
		if i < len(rows) && upgradeGroup(rows[i]) == upgradeGroup(rows[start]) {
			continue
		}
		groups = append(groups, tableFor(rows[start:i]))
		start = i
	}
	candidate := contextJSON(map[string]any{"groups": groups})
	best := compactTable(raw)
	if len(candidate) < len(best) {
		return candidate
	}
	return best
}

func upgradeGroup(row map[string]json.RawMessage) string {
	for _, key := range []string{"track", "exclusiveGroup"} {
		if value, ok := row[key]; ok {
			return key + ":" + string(value)
		}
	}
	return ""
}
