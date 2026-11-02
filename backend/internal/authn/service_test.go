package authn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testCIClientID = "0123456789abcdef0123456789abcdef.access"

func TestCloudflareServiceIdentity(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	manager := NewManager(Config{Mode: ModeCloudflare, Issuer: "https://test.cloudflareaccess.com", Audience: "app", CIClientID: testCIClientID})
	verifier := manager.cloudflare
	verifier.keys = map[string]*rsa.PublicKey{"key": &key.PublicKey}
	verifier.fetched = now
	verifier.now = func() time.Time { return now }
	claims := func() map[string]any {
		return map[string]any{"iss": verifier.Issuer, "aud": []string{"app"}, "type": "app", "sub": "", "common_name": testCIClientID, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}
	}
	sign := func(c map[string]any) string {
		payload, _ := json.Marshal(c)
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"key"}`))
		message := header + "." + base64.RawURLEncoding.EncodeToString(payload)
		digest := sha256.Sum256([]byte(message))
		return message + "." + signTestDigest(t, key, digest[:])
	}
	request := func(token string) *http.Request {
		req := httptest.NewRequest("GET", "/v1/auth/session", nil)
		req.Header.Set("Cf-Access-Jwt-Assertion", token)
		return req
	}
	actor, ok := manager.Authenticate(request(sign(claims())))
	if !ok || actor.Role != CI || actor.Email != "" || actor.ID != verifier.Issuer+"|service:"+testCIClientID || actor.CanInspect() || actor.CanManage(actor.ID) {
		t.Fatalf("unexpected service identity: %+v, %v", actor, ok)
	}
	router := http.NewServeMux()
	NewRoutes(manager).RegisterRoutes(router)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request(sign(claims())))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"role":"ci"`) || strings.Contains(response.Body.String(), `"admin":true`) {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"unknown service", func(c map[string]any) { c["common_name"] = "other.access" }},
		{"no identity", func(c map[string]any) { delete(c, "common_name") }},
		{"user subject", func(c map[string]any) { c["sub"] = "user" }},
		{"user email", func(c map[string]any) { c["email"] = "owner@example.com" }},
		{"organization token", func(c map[string]any) { c["type"] = "org" }},
		{"missing type", func(c map[string]any) { delete(c, "type") }},
		{"wrong issuer", func(c map[string]any) { c["iss"] = "https://other.cloudflareaccess.com" }},
		{"wrong audience", func(c map[string]any) { c["aud"] = []string{"other"} }},
		{"expired", func(c map[string]any) { c["exp"] = now.Unix() }},
		{"future issuance", func(c map[string]any) { c["iat"] = now.Add(time.Hour).Unix() }},
		{"not yet valid", func(c map[string]any) { c["nbf"] = now.Add(time.Hour).Unix() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := claims()
			test.change(c)
			if _, ok := manager.Authenticate(request(sign(c))); ok {
				t.Fatal("invalid service token accepted")
			}
		})
	}
	req := request(sign(claims()))
	req.Header.Add("Cf-Access-Jwt-Assertion", sign(claims()))
	if _, ok := manager.Authenticate(req); ok {
		t.Fatal("duplicate assertion accepted")
	}
	if _, ok := manager.Authenticate(request(sign(claims()) + "broken")); ok {
		t.Fatal("forged signature accepted")
	}
	verifier.CIClientID = ""
	if _, ok := manager.Authenticate(request(sign(claims()))); ok {
		t.Fatal("service auth was not disabled by default")
	}
}

func TestCIReadOnlyRouteBoundary(t *testing.T) {
	actor := Actor{ID: "ci", Role: CI}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	handler := BrowserProtection([]string{"https://play.example.com"}, RoutePolicy(next))
	for _, test := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/v1/scenarios", 204}, {"GET", "/v1/auth/session", 204},
		{"HEAD", "/v1/scenarios", 403}, {"POST", "/v1/scenarios", 403},
		{"GET", "/v1/battles", 403}, {"POST", "/v1/battles", 403},
		{"POST", "/v1/battles/id/stop", 403}, {"GET", "/v1/factions", 403},
		{"GET", "/v1/admin/model-access", 403}, {"PUT", "/v1/admin/model-access", 403},
		{"GET", "/v1/battles/id/decisions", 403}, {"GET", "/v1/future", 403},
		{"POST", "/v1/auth/logout", 403}, {"POST", "/v1/auth/login", 403},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			req := httptest.NewRequest(test.method, test.path, nil)
			req.Header.Set("Origin", "https://play.example.com")
			req = req.WithContext(WithActor(context.Background(), actor))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != test.want {
				t.Fatalf("status %d, want %d", response.Code, test.want)
			}
		})
	}
	for _, permission := range []Permission{PermissionPlay, PermissionInspect, PermissionAdmin, "unknown"} {
		response := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil).WithContext(WithActor(context.Background(), actor))
		Permit(permission, next).ServeHTTP(response, req)
		if response.Code != 403 {
			t.Fatal("CI inherited user permission", permission)
		}
	}
}
