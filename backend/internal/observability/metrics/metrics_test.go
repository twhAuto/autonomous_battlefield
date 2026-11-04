// Checks application metrics are recorded with bounded labels and exposed through the
// Prometheus handler.

package metrics

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// A labelled series that first appears at 1 shows no increase to rate() or increase().
// Initialisation must create each one at 0, so its first event counts.
func TestInitSeriesStartEveryKnownLabelAtZero(t *testing.T) {
	for _, collector := range []interface{ Reset() }{engineClaims, engineBattlesProcessed, engineBattleDuration, resultCacheOperations} {
		collector.Reset()
	}
	families := map[string]int{
		"battlefield_engine_battle_claims_total":                2,
		"battlefield_engine_battles_processed_total":            len(battleOutcomes),
		"battlefield_engine_battle_processing_duration_seconds": len(battleOutcomes),
		"battlefield_result_cache_operations_total":             5,
	}
	for family := range families {
		if series := exposedSeries(t, family); len(series) != 0 {
			t.Fatalf("%s exposed %v before initialisation", family, series)
		}
	}

	InitEngineSeries()
	InitResultCacheSeries()
	for family, want := range families {
		series := exposedSeries(t, family)
		if len(series) != want {
			t.Errorf("%s: %d series after initialisation, want %d: %v", family, len(series), want, series)
		}
		for labels, value := range series {
			if value != 0 {
				t.Errorf("%s{%s} starts at %v, want 0", family, labels, value)
			}
		}
	}

	ObserveEngineBattle(BattleFailed, time.Second)
	if got := exposedSeries(t, "battlefield_engine_battles_processed_total")["outcome="+BattleFailed]; got != 1 {
		t.Fatalf("first failure moved the series to %v, want 1", got)
	}
}

// exposedSeries gathers one metric family, mapping each series' labels to its counter
// value or, for a histogram, its sample count.
func exposedSeries(t *testing.T, name string) map[string]float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	series := map[string]float64{}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			var labels []string
			for _, pair := range metric.GetLabel() {
				labels = append(labels, pair.GetName()+"="+pair.GetValue())
			}
			value := metric.GetCounter().GetValue()
			if histogram := metric.GetHistogram(); histogram != nil {
				value = float64(histogram.GetSampleCount())
			}
			series[strings.Join(labels, ",")] = value
		}
	}
	return series
}

func TestApplicationMetricsAreRecordedAndExposed(t *testing.T) {
	ObserveHTTPRequest(http.MethodGet, "", http.StatusNotFound, 2*time.Millisecond)
	ObserveHTTPRequest(http.MethodPost, "POST /v1/battles", http.StatusAccepted, 3*time.Millisecond)
	ObserveHTTPRequest(http.MethodGet, "GET /v1/battles", http.StatusInternalServerError, time.Millisecond)
	ObserveEngineClaim(false)
	ObserveEngineClaim(true)
	ObserveEngineBattle("completed", time.Second)
	ObserveEngineTick()
	SetEngineReady(true)
	SetEngineReady(false)
	SetEngineSlots(4)
	TrackEngineBattle()()
	finish := TrackEngineBattle()
	defer finish()

	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", response.Code, http.StatusOK)
	}
	for _, metric := range []string{
		"battlefield_http_server_requests_total",
		`route="unmatched"`,
		"battlefield_engine_battle_claims_total",
		"battlefield_engine_battles_processed_total",
		"battlefield_engine_ticks_total",
		"battlefield_engine_worker_ready 0",
		"battlefield_engine_battle_slots 4",
		"battlefield_engine_active_battles 1",
		`battlefield_http_server_request_duration_seconds_count{method="GET",outcome="error",route="GET /v1/battles"} 1`,
		`battlefield_http_server_request_duration_seconds_count{method="POST",outcome="success",route="POST /v1/battles"} 1`,
	} {
		if !strings.Contains(response.Body.String(), metric) {
			t.Fatalf("metrics output does not contain %q", metric)
		}
	}
}

// The longest default battle (backend/gamedata/battlefield.yaml with the deployed
// decision timeout): 800 one-second ticks, plus a 3-minute wait at each of the 40
// decision windows that open every 20 ticks.
const longestDefaultBattle = 800*time.Second + 40*3*time.Minute

func TestBattleDurationBucketsCoverTheLongestDefaultBattle(t *testing.T) {
	// A label of its own, so other tests' observations cannot satisfy the check.
	ObserveEngineBattle("longest-default", longestDefaultBattle)

	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	bucket := regexp.MustCompile(`(?m)^battlefield_engine_battle_processing_duration_seconds_bucket\{outcome="longest-default",le="([^"]+)"\} (\d+)$`)
	matches := bucket.FindAllStringSubmatch(response.Body.String(), -1)
	if len(matches) < 2 {
		t.Fatalf("battle duration buckets are not exposed: %q", matches)
	}
	top, count := matches[len(matches)-2][1], matches[len(matches)-2][2] // the last finite bucket, before +Inf
	if bound, err := strconv.ParseFloat(top, 64); err != nil || bound < longestDefaultBattle.Seconds() || count != "1" {
		t.Fatalf("top finite bucket le=%s holds %s battles; want a bound of at least %v holding the longest default battle", top, count, longestDefaultBattle)
	}
}

func TestNonStandardMethodsShareOneLabel(t *testing.T) {
	for _, method := range []string{"BREW", "brew", "PROPFIND", strings.Repeat("X", 4096)} {
		ObserveHTTPRequest(method, "", http.StatusMethodNotAllowed, time.Millisecond)
	}
	ObserveHTTPRequest(http.MethodPatch, "", http.StatusMethodNotAllowed, time.Millisecond)

	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	if !strings.Contains(body, `battlefield_http_server_requests_total{code="405",method="other",route="unmatched"} 4`) {
		t.Fatalf("non-standard methods not collapsed:\n%s", body)
	}
	if !strings.Contains(body, `battlefield_http_server_requests_total{code="405",method="PATCH",route="unmatched"} 1`) {
		t.Fatal("standard method lost its label")
	}
	for _, leaked := range []string{"BREW", "brew", "PROPFIND", "XXXX"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("method %q became a label", leaked)
		}
	}
}
