package authn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

type managedPlayerFixture struct {
	t       *testing.T
	key     *rsa.PrivateKey
	now     time.Time
	allowed bool
	calls   int
	cfg     Config
	manager *Manager
}

func newManagedPlayerFixture(t *testing.T) *managedPlayerFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &managedPlayerFixture{t: t, key: key, now: time.Now(), allowed: true}
	f.cfg = Config{
		Mode: ModeCloudflare, Issuer: "https://team.cloudflareaccess.com", Audience: "game",
		Roles: map[string]Role{"owner@example.com": Admin, "dev@example.com": Developer, "old@example.com": Player},
		ResolvePlayer: func(ctx context.Context, email string) bool {
			f.calls++
			return ctx.Err() == nil && email == "friend@example.com" && f.allowed
		},
	}
	f.manager = NewManager(f.cfg)
	f.manager.cloudflare.keys = map[string]*rsa.PublicKey{"key": &key.PublicKey}
	f.manager.cloudflare.fetched = f.now
	return f
}

func (f *managedPlayerFixture) token(email, audience, subject string) string {
	encode := func(v any) string { raw, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(raw) }
	content := encode(map[string]string{"alg": "RS256", "kid": "key"}) + "." + encode(map[string]any{"iss": f.cfg.Issuer, "aud": audience, "sub": subject, "email": email, "iat": f.now.Unix(), "exp": f.now.Add(time.Hour).Unix()})
	digest := sha256.Sum256([]byte(content))
	return content + "." + signTestDigest(f.t, f.key, digest[:])
}

func (f *managedPlayerFixture) authenticate(jwt string) (Actor, bool) {
	r := httptest.NewRequest("GET", "/v1/auth/session", nil)
	r.Header.Set("Cf-Access-Jwt-Assertion", jwt)
	return f.manager.Authenticate(r)
}

func TestManagedPlayersRequireCurrentMembership(t *testing.T) {
	f := newManagedPlayerFixture(t)
	jwt := f.token("Friend@example.com", "game", "stable-subject")
	actor, ok := f.authenticate(jwt)
	if !ok || actor.Role != Player || actor.ID != f.cfg.Issuer+"|stable-subject" {
		t.Fatal(actor, ok)
	}
	f.allowed = false
	if _, ok = f.authenticate(jwt); ok {
		t.Fatal("cached membership allowed removed player")
	}
	if _, ok = f.authenticate(f.token("old@example.com", "game", "old")); ok {
		t.Fatal("static Player fallback")
	}
}

func TestManagedPlayersVerifyJWTBeforeMembershipLookup(t *testing.T) {
	f := newManagedPlayerFixture(t)
	jwt := f.token("friend@example.com", "game", "stable-subject")
	for _, bad := range []string{jwt + "x", f.token("friend@example.com", "other", "subject"), f.token("friend@example.com", "game", "")} {
		if _, ok := f.authenticate(bad); ok {
			t.Fatal("invalid token accepted")
		}
	}
	if f.calls != 0 {
		t.Fatal("membership queried before verification")
	}
}

func TestManagedPlayersPreservePrivilegedAndCIAuthentication(t *testing.T) {
	f := newManagedPlayerFixture(t)
	for email, role := range map[string]Role{"owner@example.com": Admin, "dev@example.com": Developer} {
		a, ok := f.authenticate(f.token(email, "game", "privileged"))
		if !ok || a.Role != role {
			t.Fatal("privileged role changed", a)
		}
	}
	if f.calls != 0 {
		t.Fatal("privileged auth depends on membership database")
	}
	f.manager.cloudflare.CIClientID = "service"
	if a, ok := f.manager.cloudflare.actorWithContext(context.Background(), accessClaims{CommonName: "service", Type: "app"}); !ok || a.Role != CI {
		t.Fatal("CI broken", a)
	}
	if f.calls != 0 {
		t.Fatal("CI queried member database")
	}
}

func TestManagedPlayerSessionCapability(t *testing.T) {
	f := newManagedPlayerFixture(t)
	f.assertMembersCapability(true)
	f.manager.config.ResolvePlayer = nil
	f.assertMembersCapability(false)
}

func (f *managedPlayerFixture) assertMembersCapability(want bool) {
	f.t.Helper()
	r := httptest.NewRequest("GET", "/v1/auth/session", nil)
	r.Header.Set("Cf-Access-Jwt-Assertion", f.token("owner@example.com", "game", "admin"))
	w := httptest.NewRecorder()
	NewRoutes(f.manager).session(w, r)
	var session struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if json.Unmarshal(w.Body.Bytes(), &session) != nil {
		f.t.Fatal("invalid session response", w.Body.String())
	}
	_, hasCLI := session.Capabilities["cli"]
	if session.Capabilities["members"] != want || hasCLI {
		f.t.Fatal("incorrect members capability", w.Body.String())
	}
}
