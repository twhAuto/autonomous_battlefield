// Package metrics owns the application's bounded-cardinality Prometheus
// metrics. Metric labels must describe a finite set of values; identifiers such
// as request IDs, battle IDs, faction IDs, and worker IDs do not belong here.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// engineBattleDurationBuckets (seconds) run from failures in the first seconds to the
// longest default battle: 800 one-second ticks plus the 3-minute decision timeout at
// each of its 40 windows, about 8,000 s. They are densest around typical battles of
// 10 to 20 minutes. Longer battles, with custom tick counts or user pauses, fall in +Inf.
var engineBattleDurationBuckets = []float64{1, 5, 15, 30, 60, 120, 300, 600, 900, 1200, 1800, 2700, 3600, 5400, 7200, 9000}

// Battle processing outcomes. Every attempt the engine claims ends with exactly one.
const (
	BattleCompleted = "completed"
	BattleFailed    = "failed"
	BattleError     = "error"
	BattleLeaseLost = "lease_lost"
	BattleRequeued  = "requeued"
)

var battleOutcomes = []string{BattleCompleted, BattleFailed, BattleError, BattleLeaseLost, BattleRequeued}

// resultCacheOutcomes lists the outcomes each result-cache operation records.
var resultCacheOutcomes = map[string][]string{"get": {"hit", "miss", "error"}, "set": {"stored", "error"}}

var (
	httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "battlefield",
		Subsystem: "http_server",
		Name:      "requests_total",
		Help:      "Total API HTTP requests by method, normalized route, and response code.",
	}, []string{"method", "route", "code"})
	httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "battlefield",
		Subsystem: "http_server",
		Name:      "request_duration_seconds",
		Help:      "API HTTP request duration by method, normalized route, and success/error outcome.",
		Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"method", "route", "outcome"})
	engineClaims = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "battlefield",
		Subsystem: "engine",
		Name:      "battle_claims_total",
		Help:      "Total battles claimed by an engine worker.",
	}, []string{"reclaimed"})
	engineBattlesProcessed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "battlefield",
		Subsystem: "engine",
		Name:      "battles_processed_total",
		Help:      "Total battle processing attempts by terminal worker outcome.",
	}, []string{"outcome"})
	engineBattleDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "battlefield",
		Subsystem: "engine",
		Name:      "battle_processing_duration_seconds",
		Help:      "Wall-clock time spent processing a claimed battle by outcome.",
		Buckets:   engineBattleDurationBuckets,
	}, []string{"outcome"})
	engineTicks = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "battlefield",
		Subsystem: "engine",
		Name:      "ticks_total",
		Help:      "Total battle ticks advanced by the engine worker.",
	})
	resultCacheOperations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "battlefield",
		Subsystem: "result_cache",
		Name:      "operations_total",
		Help:      "Completed-battle Redis cache operations by operation (get, set) and outcome (hit, miss, stored, error).",
	}, []string{"operation", "outcome"})
	engineReady = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "battlefield",
		Subsystem: "engine",
		Name:      "worker_ready",
		Help:      "Whether this engine worker is ready to claim and process battles.",
	})
	engineSlots = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "battlefield",
		Subsystem: "engine",
		Name:      "battle_slots",
		Help:      "How many battles this engine worker can run at once.",
	})
	engineActiveBattles = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "battlefield",
		Subsystem: "engine",
		Name:      "active_battles",
		Help:      "Battles this engine worker is running now.",
	})
)

func init() {
	prometheus.MustRegister(
		httpRequests,
		httpRequestDuration,
		engineClaims,
		engineBattlesProcessed,
		engineBattleDuration,
		engineTicks,
		engineReady,
		engineSlots,
		engineActiveBattles,
		resultCacheOperations,
	)
}

// Handler exposes both application metrics and the Go/process collectors
// registered by the Prometheus client.
func Handler() http.Handler {
	return promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

// ObserveHTTPRequest records one served API request under its route pattern, or
// "unmatched" when no route matched.
func ObserveHTTPRequest(method, route string, code int, duration time.Duration) {
	if route == "" {
		route = "unmatched"
	}
	method = requestMethod(method)
	httpRequests.WithLabelValues(method, route, strconv.Itoa(code)).Inc()
	httpRequestDuration.WithLabelValues(method, route, requestOutcome(code)).Observe(duration.Seconds())
}

// standardMethods are the request methods kept as metric labels.
var standardMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true,
	http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true,
	http.MethodConnect: true, http.MethodOptions: true, http.MethodTrace: true,
}

// requestMethod collapses any non-standard method to "other". The server accepts any
// method token a client sends, so a raw label would let callers create series without
// limit.
func requestMethod(method string) string {
	if standardMethods[method] {
		return method
	}
	return "other"
}

// requestOutcome collapses a response code into a bounded success/error label so
// latency can be measured over served requests without multiplying histogram
// series by every status code. A fast 5xx must not read as a fast success.
func requestOutcome(code int) string {
	if code >= 500 {
		return "error"
	}
	return "success"
}

// ObserveEngineClaim counts a battle claim, separating reclaimed expired leases.
func ObserveEngineClaim(reclaimed bool) {
	engineClaims.WithLabelValues(strconv.FormatBool(reclaimed)).Inc()
}

// ObserveEngineBattle records a battle attempt's terminal outcome and duration.
func ObserveEngineBattle(outcome string, duration time.Duration) {
	engineBattlesProcessed.WithLabelValues(outcome).Inc()
	engineBattleDuration.WithLabelValues(outcome).Observe(duration.Seconds())
}

// InitEngineSeries creates every claim and battle outcome series at zero. A labelled
// series otherwise appears only with its first event, already at 1, and rate() and
// increase() see no change from it: the first claim and the first outcome of each kind
// after an engine restart would be missing from dashboards, SLIs and alerts.
func InitEngineSeries() {
	for _, reclaimed := range []bool{false, true} {
		engineClaims.WithLabelValues(strconv.FormatBool(reclaimed))
	}
	for _, outcome := range battleOutcomes {
		engineBattlesProcessed.WithLabelValues(outcome)
		engineBattleDuration.WithLabelValues(outcome)
	}
}

// InitResultCacheSeries creates every result-cache operation and outcome series at zero,
// for the reason given on InitEngineSeries.
func InitResultCacheSeries() {
	for operation, outcomes := range resultCacheOutcomes {
		for _, outcome := range outcomes {
			resultCacheOperations.WithLabelValues(operation, outcome)
		}
	}
}

// ObserveEngineTick counts one advanced battle tick.
func ObserveEngineTick() {
	engineTicks.Inc()
}

// ObserveResultCache records one completed-battle cache operation. The API answers
// from PostgreSQL whenever the cache misses or fails, so errors here are visible only
// through this metric.
func ObserveResultCache(operation, outcome string) {
	resultCacheOperations.WithLabelValues(operation, outcome).Inc()
}

// SetEngineSlots reports how many battles this engine worker can run at once.
func SetEngineSlots(slots int) {
	engineSlots.Set(float64(slots))
}

// TrackEngineBattle counts a battle as active until the returned function is called.
func TrackEngineBattle() func() {
	engineActiveBattles.Inc()
	return engineActiveBattles.Dec
}

// SetEngineReady reports whether this engine worker can claim battles.
func SetEngineReady(ready bool) {
	if ready {
		engineReady.Set(1)
		return
	}
	engineReady.Set(0)
}
