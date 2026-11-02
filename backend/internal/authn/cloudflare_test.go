package authn

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCloudflareIdentityAndKeyRotation(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2000000000, 0)
	kid := "one"
	calls := 0
	encode := func(v any) string { raw, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(raw) }
	verifier := &Cloudflare{Issuer: "https://test.cloudflareaccess.com", Audience: "battlefield", Roles: map[string]Role{"friend@example.com": Player}, now: func() time.Time { return now }}
	verifier.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != verifier.Issuer+"/cdn-cgi/access/certs" {
			t.Fatal(r.URL)
		}
		raw, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kid": kid, "kty": "RSA", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}
	sign := func(claims map[string]any, alg string) string {
		payload := encode(map[string]string{"alg": alg, "kid": kid}) + "." + encode(claims)
		digest := sha256.Sum256([]byte(payload))
		return payload + "." + signTestDigest(t, key, digest[:])
	}
	claims := func() map[string]any {
		return map[string]any{"iss": verifier.Issuer, "sub": "subject", "email": "Friend@example.com", "aud": []string{"battlefield"}, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "type": "app"}
	}
	request := func(token string) *http.Request {
		r := httptest.NewRequest("GET", "/v1/battles", nil)
		r.Header.Set("Cf-Access-Jwt-Assertion", token)
		return r
	}
	valid := sign(claims(), "RS256")
	actor, ok := verifier.Authenticate(request(valid))
	if !ok || actor.ID != verifier.Issuer+"|subject" || actor.Role != Player {
		t.Fatal(actor, ok)
	}
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"expired", func(c map[string]any) { c["exp"] = now.Unix() }},
		{"audience", func(c map[string]any) { c["aud"] = []string{"another-app"} }},
		{"issuer", func(c map[string]any) { c["iss"] = "https://attacker.example" }},
		{"unassigned", func(c map[string]any) { c["email"] = "stranger@example.com" }},
		{"subject", func(c map[string]any) { delete(c, "sub") }},
		{"future", func(c map[string]any) { c["nbf"] = now.Add(time.Hour).Unix() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := claims()
			test.change(c)
			if _, ok := verifier.Authenticate(request(sign(c, "RS256"))); ok {
				t.Fatal("invalid token accepted")
			}
		})
	}
	checkMalformedTokens(t, verifier, request, []string{"", valid + "x", sign(claims(), "HS256"), "bad.token"})
	if calls != 1 {
		t.Fatal("valid cached key refetched", calls)
	}
	kid = "two"
	if _, ok := verifier.Authenticate(request(sign(claims(), "RS256"))); ok {
		t.Fatal("refresh was not bounded")
	}
	now = now.Add(time.Minute)
	if _, ok := verifier.Authenticate(request(sign(claims(), "RS256"))); !ok {
		t.Fatal("rotation failed")
	}
	delete(verifier.Roles, "friend@example.com")
	if _, ok := verifier.Authenticate(request(sign(claims(), "RS256"))); ok {
		t.Fatal("removed role retained")
	}
	now = now.Add(2 * time.Hour)
	verifier.Client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if _, ok := verifier.Authenticate(request(sign(claims(), "RS256"))); ok {
		t.Fatal("expired keys accepted during outage")
	}
}

func TestProductionNeverUsesLegacyCredentials(t *testing.T) {
	m := NewManager(Config{Mode: "cloudflare", Username: "admin", Password: "password", SessionSecret: "secret", SessionTTL: time.Hour})
	r := httptest.NewRequest("GET", "/v1/auth/session", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: m.createToken(Actor{ID: "admin"}, time.Now().Add(time.Hour))})
	r.Header.Set("Cf-Access-Authenticated-User-Email", "admin@example.com")
	if _, ok := m.Authenticate(r); ok {
		t.Fatal("legacy cookie/header accepted")
	}
	router := http.NewServeMux()
	NewRoutes(m).RegisterRoutes(router)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(`{"username":"admin","password":"password"}`)))
	if w.Code != 404 || w.Header().Get("Set-Cookie") != "" {
		t.Fatal(w.Code)
	}
	// The disabled login route must look like any other absent route.
	var envelope struct {
		Error apierror.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != apierror.CodeNotFound || envelope.Error.Message != "route not found" {
		t.Fatalf("login in Cloudflare mode = %q (%v), want not_found / route not found", w.Body.String(), err)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/v1/auth/logout", nil))
	if !strings.Contains(w.Body.String(), "/cdn-cgi/access/logout") || w.Header().Get("Set-Cookie") != "" {
		t.Fatal(w.Body.String())
	}
}
func TestRouteRolesAndOrigins(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	for _, test := range []struct {
		role                          Role
		path, method, origin, content string
		want                          int
	}{
		{Player, "/v1/battles", "GET", "", "", 204},
		{Player, "/v1/battles/b/decisions", "GET", "", "", 204},
		{Player, "/v1/battles/b/decisions/r/context", "GET", "", "", 403},
		{Tester, "/v1/battles/b/decisions/r/context", "GET", "", "", 204},
		{Admin, "/v1/new-admin-route", "GET", "", "", 403},
		{Player, "/v1/factions", "POST", "https://evil.example", "application/json", 403},
		{Admin, "/v1/factions", "POST", "", "application/json", 403},
		{Player, "/v1/factions", "POST", "https://battle.example", "text/plain", 415},
		{Player, "/v1/factions", "POST", "https://battle.example", "application/json", 204},
	} {
		r := httptest.NewRequest(test.method, test.path, strings.NewReader("{}"))
		r = r.WithContext(WithActor(context.Background(), Actor{ID: "friend", Role: test.role}))
		r.Header.Set("Origin", test.origin)
		r.Header.Set("Content-Type", test.content)
		w := httptest.NewRecorder()
		BrowserProtection("https://battle.example", RoutePolicy(next)).ServeHTTP(w, r)
		if w.Code != test.want {
			t.Fatalf("%+v got %d", test, w.Code)
		}
	}
	if (Actor{ID: "friend", Role: Player}).CanManage("other") {
		t.Fatal("cross-owner permission")
	}
}

func checkMalformedTokens(t *testing.T, verifier *Cloudflare, request func(string) *http.Request, tokens []string) {
	t.Helper()
	for _, token := range tokens {
		if _, ok := verifier.Authenticate(request(token)); ok {
			t.Fatal("malformed token accepted")
		}
	}
}

func TestSigningKeysRejectInvalidAndDuplicateEntries(t *testing.T) {
	n := base64.RawURLEncoding.EncodeToString(new(big.Int).Lsh(big.NewInt(1), 2047).Bytes())
	valid := signingKey{Kid: "one", Kty: "RSA", Alg: "RS256", Use: "sig", N: n, E: "AQAB"}
	for _, tt := range []struct {
		name   string
		change func(*signingKey)
	}{
		{"key type", func(k *signingKey) { k.Kty = "EC" }},
		{"missing id", func(k *signingKey) { k.Kid = "" }},
		{"algorithm", func(k *signingKey) { k.Alg = "HS256" }},
		{"encryption key", func(k *signingKey) { k.Use = "enc" }},
		{"small modulus", func(k *signingKey) { k.N = "AQAB" }},
		{"invalid exponent", func(k *signingKey) { k.E = "!" }},
		{"even exponent", func(k *signingKey) { k.E = "BA" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bad := valid
			tt.change(&bad)
			if len(parseSigningKeys([]signingKey{bad})) != 0 {
				t.Fatal("unsafe key accepted")
			}
		})
	}
	if keys := parseSigningKeys([]signingKey{valid, valid}); keys != nil {
		t.Fatal("duplicate key IDs accepted")
	}
	if keys := parseSigningKeys([]signingKey{valid}); keys["one"] == nil {
		t.Fatal("valid key rejected")
	}
}

func signTestDigest(t *testing.T, key *rsa.PrivateKey, digest []byte) string {
	t.Helper()
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(signature)
}
