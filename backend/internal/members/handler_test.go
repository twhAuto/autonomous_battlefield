package members

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
)

type fakeRepository struct {
	err              error
	actor, email, id string
	grant            bool
	calls            int
}

func (f *fakeRepository) List(context.Context) ([]Member, string, error) {
	return []Member{}, "", f.err
}
func (f *fakeRepository) Change(_ context.Context, actor, email, id string, grant bool) error {
	f.calls++
	f.actor, f.email, f.id, f.grant = actor, email, id, grant
	return f.err
}
func (f *fakeRepository) Retry(_ context.Context, actor string) error {
	f.calls++
	f.actor = actor
	return f.err
}
func request(h *Handler, method, path, body string, role authn.Role, origin, preview string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Battlefield-Preview-Role", preview)
	if role != "" {
		r = r.WithContext(authn.WithActor(r.Context(), authn.Actor{ID: "verified-admin", Role: role}))
	}
	w := httptest.NewRecorder()
	authn.BrowserProtection("https://game.example", authn.RolePreview(authn.RoutePolicy(mux))).ServeHTTP(w, r)
	return w
}

type memberRoute struct{ method, path, body string }

func memberRoutes() []memberRoute {
	return []memberRoute{{"GET", "/v1/admin/members", ""}, {"POST", "/v1/admin/members", `{"email":"friend@example.com"}`}, {"DELETE", "/v1/admin/members/id", ""}, {"POST", "/v1/admin/members/retry", ""}}
}

func TestMemberRoutesRequireAdmin(t *testing.T) {
	f := &fakeRepository{}
	h := NewHandler(f, "https://game.example")
	for _, tt := range memberRoutes() {
		for _, role := range []authn.Role{"", authn.Player, authn.Tester, authn.CI} {
			w := request(h, tt.method, tt.path, tt.body, role, "https://game.example", "")
			if w.Code != 401 && w.Code != 403 {
				t.Fatal("unauthorized route", role, w.Code)
			}
		}
	}
	if f.calls != 0 {
		t.Fatal("unauthorized writes", f.calls)
	}
}

func TestMemberRoutesServeAdmin(t *testing.T) {
	f := &fakeRepository{}
	h := NewHandler(f, "https://game.example")
	for _, tt := range memberRoutes() {
		w := request(h, tt.method, tt.path, tt.body, authn.Admin, "https://game.example", "")
		if w.Code != 200 && w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable members")
		}
		if tt.method != "GET" && f.actor != "verified-admin" {
			t.Fatal("write did not use verified administrator", tt.method, tt.path, f.actor)
		}
	}
	if f.calls != 3 {
		t.Fatal("unexpected write count", f.calls)
	}
}

func TestMemberRouteWritesEnforceOrigin(t *testing.T) {
	h := NewHandler(&fakeRepository{}, "https://game.example")
	for _, tt := range memberRoutes() {
		if tt.method == "GET" {
			continue
		}
		if request(h, tt.method, tt.path, tt.body, authn.Admin, "https://evil.example", "").Code != 403 {
			t.Fatal("origin bypass", tt.method, tt.path)
		}
	}
}

func TestMemberRoutesAreHiddenDuringPreview(t *testing.T) {
	h := NewHandler(&fakeRepository{}, "https://game.example")
	for _, tt := range memberRoutes() {
		if request(h, tt.method, tt.path, tt.body, authn.Admin, "https://game.example", "player").Code != 403 {
			t.Fatal("preview bypass", tt.method, tt.path)
		}
	}
}

func TestMemberGrantRejectsInvalidBodies(t *testing.T) {
	h := NewHandler(&fakeRepository{}, "https://game.example")
	for _, body := range []string{`{"email":"x","role":"admin"}`, `{"email":"x"}{}`, strings.Repeat("x", 4097)} {
		if request(h, "POST", "/v1/admin/members", body, authn.Admin, "https://game.example", "").Code != 400 {
			t.Fatal("body accepted", body)
		}
	}
}

func TestMemberErrorsMapToSafeResponses(t *testing.T) {
	f := &fakeRepository{}
	h := NewHandler(f, "https://game.example")
	for _, tt := range []struct {
		err  error
		code int
	}{{ErrInvalid, 400}, {ErrProtected, 409}, {ErrNotFound, 404}, {errors.New("database credential"), 503}} {
		f.err = tt.err
		w := request(h, "POST", "/v1/admin/members", `{"email":"x"}`, authn.Admin, "https://game.example", "")
		if w.Code != tt.code || strings.Contains(w.Body.String(), "credential") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestMemberServiceFailuresAreUnavailable(t *testing.T) {
	f := &fakeRepository{err: errors.New("db")}
	h := NewHandler(f, "https://game.example")
	if request(h, "GET", "/v1/admin/members", "", authn.Admin, "https://game.example", "").Code != 503 {
		t.Fatal("read error hidden")
	}
	if request(h, "POST", "/v1/admin/members/retry", "", authn.Admin, "https://game.example", "").Code != 503 {
		t.Fatal("retry error hidden")
	}
	if request(h, "DELETE", "/v1/admin/members/id", "", authn.Admin, "https://game.example", "").Code != 503 {
		t.Fatal("remove error hidden")
	}
}

func TestMemberRoutesAreUnavailableWithoutStore(t *testing.T) {
	h := NewHandler(nil, "https://game.example")
	if request(h, "GET", "/v1/admin/members", "", authn.Admin, "https://game.example", "").Code != 503 {
		t.Fatal("disabled management allowed")
	}
}

func TestUnexpectedFailuresAreLoggedWithoutLeaking(t *testing.T) {
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(original)
	secret := "failed to connect to `user=battlefield database=battlefield host=10.0.0.5`"
	for _, tc := range []struct {
		err      error
		reason   string
		sqlstate string
	}{
		{errors.New(secret), "database_unavailable", ""},
		{fmt.Errorf("list: %w", &pgconn.PgError{Code: "57P01", Message: secret}), "database_error", "57P01"},
		{fmt.Errorf("%s: %w", secret, context.DeadlineExceeded), "deadline_exceeded", ""},
		{fmt.Errorf("%s: %w", secret, context.Canceled), "canceled", ""},
	} {
		logs.Reset()
		response := httptest.NewRecorder()
		memberError(response, httptest.NewRequest(http.MethodGet, "/v1/admin/members", nil), tc.err)
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "10.0.0.5") {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
		got := logs.String()
		if !strings.Contains(got, `"msg":"player_members_request_failed"`) || !strings.Contains(got, `"reason":"`+tc.reason+`"`) {
			t.Fatalf("failure not categorized as %s: %s", tc.reason, got)
		}
		if tc.sqlstate != "" && !strings.Contains(got, `"sqlstate":"`+tc.sqlstate+`"`) {
			t.Fatalf("SQLSTATE missing: %s", got)
		}
		if strings.Contains(got, "10.0.0.5") || strings.Contains(got, "user=battlefield") {
			t.Fatalf("log leaked connection details: %s", got)
		}
	}
}
