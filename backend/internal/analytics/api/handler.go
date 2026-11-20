// Serves the analytics report and translates its query parameters.

package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	analyticsservice "github.com/twhAuto/autonomous_battlefield/backend/internal/analytics/service"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/apierror"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/httpapi"
)

type Handler struct {
	analytics *analyticsservice.Service
}

func NewHandler(analytics *analyticsservice.Service) *Handler {
	return &Handler{analytics: analytics}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/analytics", h.GetReport)
}

func (h *Handler) GetReport(w http.ResponseWriter, r *http.Request) {
	query, ok := parseQuery(r)
	if !ok {
		apierror.Write(w, r, http.StatusBadRequest, "invalid analytics query")
		return
	}
	report, err := h.analytics.Report(r.Context(), query)
	switch {
	case errors.Is(err, authn.ErrForbidden):
		apierror.Write(w, r, http.StatusForbidden, "permission denied")
	case errors.Is(err, analyticsservice.ErrInvalidQuery):
		apierror.Write(w, r, http.StatusBadRequest, "invalid analytics query")
	case err != nil:
		slog.ErrorContext(r.Context(), "analytics_report_failed", "request_id", httpapi.RequestID(r.Context()), "error", err)
		apierror.Write(w, r, http.StatusInternalServerError, "could not build analytics report")
	default:
		_ = httpapi.WriteJSON(w, http.StatusOK, report)
	}
}

func parseQuery(r *http.Request) (analyticsservice.Query, bool) {
	values := r.URL.Query()
	query := analyticsservice.Query{LeagueID: values.Get("leagueId"), RulesVersion: values.Get("rulesVersion")}
	if raw := values.Get("since"); raw != "" {
		since, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return query, false
		}
		query.Since = &since
	}
	if raw := values.Get("excludeFallback"); raw != "" {
		exclude, err := strconv.ParseBool(raw)
		if err != nil {
			return query, false
		}
		query.ExcludeFallback = exclude
	}
	return query, true
}
