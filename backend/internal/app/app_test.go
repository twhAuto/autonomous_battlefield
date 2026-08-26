package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/server/catalog"
)

func TestNewAPIComposesHTTPApplication(t *testing.T) {
	application := NewAPI(appDatabase{}, &catalog.Catalog{}, 10_000)
	response := httptest.NewRecorder()
	application.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", response.Code, http.StatusOK)
	}
}

type appDatabase struct{}

func (appDatabase) Ping(context.Context) error { return nil }
func (appDatabase) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (appDatabase) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (appDatabase) QueryRow(context.Context, string, ...any) pgx.Row        { return nil }
