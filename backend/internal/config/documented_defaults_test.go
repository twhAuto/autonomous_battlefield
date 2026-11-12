// Checks that the defaults documented in the system architecture match the values
// the loaders apply when the configuration file omits a key.

package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const settingsTableHeader = "| Setting | Default when omitted | Effect |"

var settingName = regexp.MustCompile("`([A-Za-z.]+)`")

// loaderDefaults returns what each documented setting resolves to for an empty
// configuration file: a time.Duration or an int.
func loaderDefaults(t *testing.T) map[string]any {
	t.Helper()
	t.Setenv(envAgentSecret, strings.Repeat("w", 32))
	var raw fileConfig
	api, err := parseAPIConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := parseEngineConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	decisionTimeout, err := parseDecisionTimeout(raw)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := parseAgent(raw)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := parseResultCacheConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"api.maxTicks":             int(api.MaxTicks),
		"engine.pollInterval":      engine.PollInterval,
		"engine.leaseDuration":     engine.LeaseDuration,
		"engine.heartbeatInterval": engine.HeartbeatInterval,
		"engine.concurrency":       engine.Concurrency,
		"engine.maxAttempts":       engine.MaxAttempts,
		"engine.shutdownTimeout":   engine.ShutdownTimeout,
		"ai.decisionTimeout":       decisionTimeout,
		"ai.agentConcurrency":      agent.Concurrency,
		"resultCache.ttl":          cache.TTL,
		"resultCache.timeout":      cache.Timeout,
	}
}

// documentedDefaults reads the settings table: each setting named in a row's first
// cell takes the leading token of the matching " / "-separated part of its second.
func documentedDefaults(t *testing.T, document string) map[string]string {
	t.Helper()
	_, table, found := strings.Cut(document, settingsTableHeader+"\n")
	if !found {
		t.Fatalf("settings table header %q not found", settingsTableHeader)
	}
	documented := map[string]string{}
	for _, row := range strings.Split(table, "\n")[1:] { // skip the |---| separator
		if !strings.HasPrefix(row, "|") {
			break
		}
		cells := strings.Split(row, "|")
		if len(cells) < 4 {
			t.Fatalf("settings row %q has too few cells", row)
		}
		names := settingName.FindAllStringSubmatch(cells[1], -1)
		values := strings.Split(cells[2], " / ")
		if len(names) == 0 || len(names) != len(values) {
			t.Fatalf("settings row %q names %d settings but gives %d defaults", row, len(names), len(values))
		}
		for i, name := range names {
			fields := strings.Fields(values[i])
			if len(fields) == 0 {
				t.Fatalf("settings row %q gives no default for %s", row, name[1])
			}
			documented[name[1]] = fields[0]
		}
	}
	return documented
}

// defaultsMismatches lists every setting whose documented default differs from the
// loader's, is documented without a loader value, or is missing from the table.
func defaultsMismatches(documented map[string]string, loaded map[string]any) []string {
	var problems []string
	for name, text := range documented {
		switch want := loaded[name].(type) {
		case nil:
			problems = append(problems, name+": documented, but this test has no loader default for it")
		case time.Duration:
			if got, err := time.ParseDuration(text); err != nil || got != want {
				problems = append(problems, name+": documented "+text+", loader applies "+want.String())
			}
		case int:
			if got, err := strconv.Atoi(text); err != nil || got != want {
				problems = append(problems, name+": documented "+text+", loader applies "+strconv.Itoa(want))
			}
		}
	}
	for name := range loaded {
		if _, ok := documented[name]; !ok {
			problems = append(problems, name+": checked by this test, but missing from the table")
		}
	}
	return problems
}

func TestDocumentedDefaultsMatchLoader(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "architecture", "system-architecture.md")
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range defaultsMismatches(documentedDefaults(t, string(document)), loaderDefaults(t)) {
		t.Errorf("%s: %s", path, problem)
	}
}

func TestDefaultsMismatchesReportsEveryKindOfDrift(t *testing.T) {
	documented := map[string]string{"a.duration": "10s", "a.count": "2", "a.unknown": "1", "a.same": "3m"}
	loaded := map[string]any{"a.duration": 20 * time.Second, "a.count": 4, "a.same": 3 * time.Minute, "a.undocumented": 1}
	problems := strings.Join(defaultsMismatches(documented, loaded), "\n")
	for _, want := range []string{
		"a.duration: documented 10s, loader applies 20s",
		"a.count: documented 2, loader applies 4",
		"a.unknown: documented, but this test has no loader default for it",
		"a.undocumented: checked by this test, but missing from the table",
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("missing %q in:\n%s", want, problems)
		}
	}
	if strings.Contains(problems, "a.same") {
		t.Errorf("matching default reported:\n%s", problems)
	}
}
