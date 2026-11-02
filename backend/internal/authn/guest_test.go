// Checks guest access: tokens for the guest Access application map to Guest whatever
// the email, guests reach only view routes, and the guest origin may send mutations
// that the route policy still denies.

package authn

import (
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func newGuestFixture(t *testing.T, guestAudience string) *managedPlayerFixture {
	t.Helper()
	f := newManagedPlayerFixture(t)
	f.cfg.GuestAudience = guestAudience
	f.manager = NewManager(f.cfg)
	f.manager.cloudflare.keys = map[string]*rsa.PublicKey{"key": &f.key.PublicKey}
	f.manager.cloudflare.fetched = f.now
	return f
}

func TestGuestTokensMapToGuestWhateverTheEmail(t *testing.T) {
	f := newGuestFixture(t, "guest-app")
	for _, email := range []string{"visitor@example.com", "owner@example.com", "tester@example.com", "friend@example.com"} {
		actor, ok := f.authenticate(f.token(email, "guest-app", "subject"))
		if !ok || actor.Role != Guest || actor.ID != f.cfg.Issuer+"|subject" {
			t.Fatal(email, actor, ok)
		}
	}
	if f.calls != 0 {
		t.Fatal("guest sign-in queried managed membership")
	}
	// The game's own audience keeps its configured roles.
	if actor, ok := f.authenticate(f.token("owner@example.com", "game", "admin")); !ok || actor.Role != Admin {
		t.Fatal("game audience", actor, ok)
	}
	for _, bad := range []string{f.token("visitor@example.com", "guest-app", ""), f.token("", "guest-app", "subject")} {
		if _, ok := f.authenticate(bad); ok {
			t.Fatal("guest token without identity accepted")
		}
	}
}

func TestGuestTokensAreRejectedUntilGuestsAreConfigured(t *testing.T) {
	f := newGuestFixture(t, "")
	if _, ok := f.authenticate(f.token("visitor@example.com", "guest-app", "subject")); ok {
		t.Fatal("guest audience accepted without configuration")
	}
}

func TestGuestServiceTokensAreRejected(t *testing.T) {
	f := newGuestFixture(t, "guest-app")
	f.manager.cloudflare.CIClientID = "service"
	if _, ok := f.manager.cloudflare.actorWithContext(t.Context(), accessClaims{CommonName: "service", Type: "app", Aud: accessAudience{"guest-app"}}); ok {
		t.Fatal("service token admitted through the guest application")
	}
}

func TestGuestsReachOnlyViewRoutes(t *testing.T) {
	for _, rule := range routeRules {
		if rule.public || rule.permission == "" {
			continue
		}
		want := rule.permission == PermissionView
		if got := permits(rule.permission, Actor{ID: "guest", Role: Guest}); got != want {
			t.Fatalf("%s: guest permitted = %v, want %v", rule.pattern, got, want)
		}
		if rule.permission == PermissionView && !permits(rule.permission, Actor{ID: "player", Role: Player}) {
			t.Fatalf("%s: view route denied to a Player", rule.pattern)
		}
	}
	for _, pattern := range []string{"GET /v1/model-profiles", "GET /v1/battles/allowance", "GET /v1/faction-background-generations/options", "POST /v1/battles", "POST /v1/factions"} {
		if !slices.ContainsFunc(routeRules, func(rule routeRule) bool { return rule.pattern == pattern && rule.permission != PermissionView }) {
			t.Fatalf("%s is open to guests", pattern)
		}
	}
	if (Actor{ID: "same", Role: Guest}).CanManage("same") {
		t.Fatal("guest manages its own rows")
	}
}

func TestBrowserProtectionAcceptsEachConfiguredOrigin(t *testing.T) {
	handler := BrowserProtection([]string{"https://play.example", "https://guest.example", ""}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for origin, want := range map[string]int{"https://play.example": 204, "https://guest.example": 204, "": 403, "https://attacker.example": 403} {
		r := httptest.NewRequest("POST", "/v1/auth/logout", strings.NewReader(`{}`))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("origin %q: status %d, want %d", origin, w.Code, want)
		}
	}
}
