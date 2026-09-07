// Checks login, signed-cookie sessions, logout, token tampering/expiry, and optional
// authentication middleware.

package authn

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoginSessionAndLogout(t *testing.T) {
	manager := testManager()
	router := http.NewServeMux()
	NewRoutes(manager).RegisterRoutes(router)

	login := httptest.NewRecorder()
	router.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"correct-password"}`)))
	if login.Code != http.StatusNoContent || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login status=%d cookies=%v", login.Code, login.Result().Cookies())
	}
	cookie := login.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || !cookie.Secure {
		t.Fatalf("session cookie flags = %#v", cookie)
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "/v1/auth/session", nil)
	sessionRequest.AddCookie(cookie)
	session := httptest.NewRecorder()
	router.ServeHTTP(session, sessionRequest)
	if session.Code != http.StatusOK || !strings.Contains(session.Body.String(), `"id":"admin"`) {
		t.Fatalf("session status=%d body=%s", session.Code, session.Body.String())
	}

	logout := httptest.NewRecorder()
	router.ServeHTTP(logout, httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil))
	if logout.Code != http.StatusNoContent || logout.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("logout status=%d cookies=%v", logout.Code, logout.Result().Cookies())
	}
}

func TestLoginRejectsInvalidAndMalformedCredentials(t *testing.T) {
	manager := testManager()
	router := http.NewServeMux()
	NewRoutes(manager).RegisterRoutes(router)

	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{name: "wrong password", body: `{"username":"admin","password":"wrong"}`, want: http.StatusUnauthorized},
		{name: "wrong username", body: `{"username":"other","password":"correct-password"}`, want: http.StatusUnauthorized},
		{name: "malformed", body: `{`, want: http.StatusBadRequest},
		{name: "unknown field", body: `{"username":"admin","password":"correct-password","extra":true}`, want: http.StatusBadRequest},
		{name: "trailing document", body: `{"username":"admin","password":"correct-password"}{}`, want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(test.body)))
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d", response.Code, test.want)
			}
		})
	}
}

func TestSessionRejectsMissingTamperedAndExpiredTokens(t *testing.T) {
	manager := testManager()
	router := http.NewServeMux()
	NewRoutes(manager).RegisterRoutes(router)

	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/v1/auth/session", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing session status=%d", missing.Code)
	}

	for name, value := range map[string]string{
		"tampered":  manager.createToken(Actor{ID: "admin"}, manager.now().Add(time.Hour)) + "x",
		"expired":   manager.createToken(Actor{ID: "admin"}, manager.now().Add(-time.Second)),
		"malformed": "not-a-session",
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v1/auth/session", nil)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: value})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
}

func TestAuthenticationMiddlewareProtectsOnlyApplicationAPI(t *testing.T) {
	manager := testManager()
	downstream := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/v1/") && !isPublicAuthenticationPath(request.URL.Path) {
			actor, ok := ActorFromContext(request.Context())
			if !ok || actor.ID != "admin" {
				t.Errorf("actor=%#v ok=%t", actor, ok)
			}
		}
		response.WriteHeader(http.StatusNoContent)
	})
	handler := RequireAuthentication(true, manager, downstream)

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/factions", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}

	for _, path := range []string{"/healthz", "/metrics", "/v1/auth/login", "/v1/auth/session"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("public path %s status=%d", path, response.Code)
		}
	}

	authorizedRequest := httptest.NewRequest(http.MethodGet, "/v1/factions", nil)
	authorizedRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: manager.createToken(Actor{ID: "admin"}, manager.now().Add(time.Hour))})
	authorized := httptest.NewRecorder()
	handler.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusNoContent {
		t.Fatalf("authorized status=%d", authorized.Code)
	}
}

func TestDisabledAuthenticationPreservesLocalWorkflow(t *testing.T) {
	manager := NewManager(Config{})
	routes := http.NewServeMux()
	NewRoutes(manager).RegisterRoutes(routes)
	session := httptest.NewRecorder()
	routes.ServeHTTP(session, httptest.NewRequest(http.MethodGet, "/v1/auth/session", nil))
	if session.Code != http.StatusOK || !strings.Contains(session.Body.String(), `"enabled":false`) {
		t.Fatalf("session status=%d body=%s", session.Code, session.Body.String())
	}

	called := false
	handler := RequireAuthentication(false, rejectingAuthenticator{}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/factions", nil))
	if !called {
		t.Fatal("disabled middleware did not call downstream")
	}
}

func testManager() *Manager {
	manager := NewManager(Config{
		Enabled: true, Username: "admin", Password: "correct-password",
		SessionSecret: "a-session-signing-secret-with-32-bytes", SessionTTL: time.Hour, CookieSecure: true,
	})
	manager.now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	return manager
}

type rejectingAuthenticator struct{}

func (rejectingAuthenticator) Authenticate(*http.Request) (Actor, bool) {
	return Actor{}, false
}
