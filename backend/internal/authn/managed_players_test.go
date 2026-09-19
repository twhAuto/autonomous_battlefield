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

func TestManagedPlayersRequireVerifiedJWTAndCurrentMembership(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	allowed := true
	calls := 0
	resolve := func(ctx context.Context, email string) bool {
		calls++
		return ctx.Err() == nil && email == "friend@example.com" && allowed
	}
	cfg := Config{Mode: ModeCloudflare, Issuer: "https://team.cloudflareaccess.com", Audience: "game", Roles: map[string]Role{"owner@example.com": Admin, "dev@example.com": Developer, "old@example.com": Player}, ResolvePlayer: resolve}
	manager := NewManager(cfg)
	manager.cloudflare.keys = map[string]*rsa.PublicKey{"key": &key.PublicKey}
	manager.cloudflare.fetched = now
	encode := func(v any) string { raw, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(raw) }
	token := func(email, audience, subject string) string {
		content := encode(map[string]string{"alg": "RS256", "kid": "key"}) + "." + encode(map[string]any{"iss": cfg.Issuer, "aud": audience, "sub": subject, "email": email, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()})
		digest := sha256.Sum256([]byte(content))
		return content + "." + signTestDigest(t, key, digest[:])
	}
	authenticate := func(jwt string) (Actor, bool) {
		r := httptest.NewRequest("GET", "/v1/auth/session", nil)
		r.Header.Set("Cf-Access-Jwt-Assertion", jwt)
		return manager.Authenticate(r)
	}
	jwt := token("Friend@example.com", "game", "stable-subject")
	actor, ok := authenticate(jwt)
	if !ok || actor.Role != Player || actor.ID != cfg.Issuer+"|stable-subject" {
		t.Fatal(actor, ok)
	}
	allowed = false
	if _, ok = authenticate(jwt); ok {
		t.Fatal("cached membership allowed removed player")
	}
	if _, ok = authenticate(token("old@example.com", "game", "old")); ok {
		t.Fatal("static Player fallback")
	}
	before := calls
	for _, bad := range []string{jwt + "x", token("friend@example.com", "other", "subject"), token("friend@example.com", "game", "")} {
		if _, ok = authenticate(bad); ok {
			t.Fatal("invalid token accepted")
		}
	}
	if calls != before {
		t.Fatal("membership queried before verification")
	}
	for email, role := range map[string]Role{"owner@example.com": Admin, "dev@example.com": Developer} {
		a, ok := authenticate(token(email, "game", "privileged"))
		if !ok || a.Role != role {
			t.Fatal("privileged role changed", a)
		}
	}
	if calls != before {
		t.Fatal("privileged auth depends on membership database")
	}
	manager.cloudflare.CIClientID = "service"
	if a, ok := manager.cloudflare.actorWithContext(context.Background(), accessClaims{CommonName: "service", Type: "app"}); !ok || a.Role != CI {
		t.Fatal("CI broken", a)
	}
	if calls != before {
		t.Fatal("CI queried member database")
	}
}
