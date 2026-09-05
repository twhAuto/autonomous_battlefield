package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/catalog"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

func TestNewAPIComposesHTTPApplication(t *testing.T) {
	application := NewAPI(appDatabase{}, &catalog.Catalog{}, 10_000)
	response := httptest.NewRecorder()
	lifecycle := httpapi.NewLifecycleHandler()
	lifecycle.Activate(application.HTTPHandler(), appDatabase{}, nil)
	lifecycle.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestNewAPIWithAuthenticationProtectsApplicationRoutes(t *testing.T) {
	application := NewAPIWithAuthentication(appDatabase{}, &catalog.Catalog{}, 10_000, authn.Config{
		Enabled: true, Username: "admin", Password: "a-long-test-password",
		SessionSecret: "a-test-session-secret-with-32-characters", SessionTTL: time.Hour,
	})
	response := httptest.NewRecorder()
	application.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/factions", nil))
	if response.Code != http.StatusUnauthorized || response.Header().Get("X-Request-ID") == "" {
		t.Fatalf("status=%d request ID=%q", response.Code, response.Header().Get("X-Request-ID"))
	}
}

type appDatabase struct{}

func (appDatabase) Ping(context.Context) error { return nil }
func (appDatabase) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (appDatabase) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (appDatabase) QueryRow(context.Context, string, ...any) pgx.Row        { return nil }
