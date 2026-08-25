// Package telemetry owns the application's bounded-cardinality Prometheus
// metrics. Metric labels must describe a finite set of values; identifiers such
// as request IDs, battle IDs, faction IDs, and worker IDs do not belong here.
package telemetry

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

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
		Help:      "API HTTP request duration by method and normalized route.",
		Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"method", "route"})
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
		Buckets:   prometheus.ExponentialBuckets(0.1, 2, 11),
	}, []string{"outcome"})
	engineTicks = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "battlefield",
		Subsystem: "engine",
		Name:      "ticks_total",
		Help:      "Total battle ticks advanced by the engine worker.",
	})
	engineReady = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "battlefield",
		Subsystem: "engine",
		Name:      "worker_ready",
		Help:      "Whether this engine worker is ready to claim and process battles.",
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
	)
}

// MetricsHandler exposes both application metrics and the Go/process collectors
// registered by the Prometheus client.
func MetricsHandler() http.Handler {
	return promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

func ObserveHTTPRequest(method, route string, code int, duration time.Duration) {
	if route == "" {
		route = "unmatched"
	}
	httpRequests.WithLabelValues(method, route, strconv.Itoa(code)).Inc()
	httpRequestDuration.WithLabelValues(method, route).Observe(duration.Seconds())
}

func ObserveEngineClaim(reclaimed bool) {
	engineClaims.WithLabelValues(strconv.FormatBool(reclaimed)).Inc()
}

func ObserveEngineBattle(outcome string, duration time.Duration) {
	engineBattlesProcessed.WithLabelValues(outcome).Inc()
	engineBattleDuration.WithLabelValues(outcome).Observe(duration.Seconds())
}

func ObserveEngineTick() {
	engineTicks.Inc()
}

func SetEngineReady(ready bool) {
	if ready {
		engineReady.Set(1)
		return
	}
	engineReady.Set(0)
}
