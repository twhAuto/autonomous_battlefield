package authn

import (
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

func TestSignedAudienceForms(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, identity := range []string{"service", "human"} {
		t.Run(identity, func(t *testing.T) {
			manager := NewManager(Config{Mode: ModeCloudflare, Issuer: "https://test.cloudflareaccess.com", Audience: "app", CIClientID: testCIClientID, Roles: map[string]Role{"user@example.com": Player}})
			manager.cloudflare.keys = map[string]*rsa.PublicKey{"one": &key.PublicKey}
			manager.cloudflare.fetched = now
			manager.cloudflare.now = func() time.Time { return now }
			router := http.NewServeMux()
			NewRoutes(manager).RegisterRoutes(router)
			for _, tc := range []struct {
				name     string
				audience any
				status   int
			}{
				{"single string", "app", 200}, {"single array", []string{"app"}, 200},
				{"multiple array", []string{"other", "app"}, 200},
				{"wrong string", "other", 401}, {"wrong array", []string{"other"}, 401},
				{"substring", "prefix-app-suffix", 401}, {"case sensitive", "APP", 401},
				{"whitespace", " app ", 401}, {"empty string", "", 401}, {"empty array", []string{}, 401},
				{"null", nil, 401}, {"number", 42, 401}, {"boolean", true, 401},
				{"object", map[string]string{"aud": "app"}, 401},
				{"mixed array", []any{"app", 42}, 401}, {"null item", []any{"app", nil}, 401},
				{"nested array", []any{[]string{"app"}}, 401},
			} {
				t.Run(tc.name, func(t *testing.T) {
					checkAudienceSession(t, manager, router, key, identity, tc.audience, tc.status)
				})
			}
		})
	}
}

func checkAudienceSession(t *testing.T, manager *Manager, router http.Handler, key *rsa.PrivateKey, identity string, audience any, status int) {
	t.Helper()
	now := manager.cloudflare.now()
	claims := map[string]any{"iss": manager.cloudflare.Issuer, "aud": audience, "type": "app", "iat": now.Unix(), "exp": now.Unix() + 300, "sub": ""}
	if identity == "service" {
		claims["common_name"] = testCIClientID
	} else {
		claims["sub"] = "user"
		claims["email"] = "user@example.com"
	}
	payload, _ := json.Marshal(claims)
	message := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"one"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(message))
	request := httptest.NewRequest("GET", "/v1/auth/session", nil)
	request.Header.Set("Cf-Access-Jwt-Assertion", message+"."+signTestDigest(t, key, digest[:]))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("status=%d want=%d", response.Code, status)
	}
	if identity == "service" && status == 200 && !strings.Contains(response.Body.String(), `"role":"ci"`) {
		t.Fatal("service identity lost CI role")
	}
}
