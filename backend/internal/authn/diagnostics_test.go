package authn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAuthenticationFailureReasons(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, tc := range []struct {
		reason string
		change func(map[string]any, *Cloudflare)
	}{
		{"issuer_mismatch", func(c map[string]any, _ *Cloudflare) { c["iss"] = "private-issuer" }},
		{"audience_mismatch", func(c map[string]any, _ *Cloudflare) { c["aud"] = []string{"private-audience"} }},
		{"token_expired", func(c map[string]any, _ *Cloudflare) { c["exp"] = now.Unix() }},
		{"issued_at_missing_or_invalid", func(c map[string]any, _ *Cloudflare) { delete(c, "iat") }},
		{"issued_at_in_future", func(c map[string]any, _ *Cloudflare) { c["iat"] = now.Unix() + 31 }},
		{"token_not_yet_valid", func(c map[string]any, _ *Cloudflare) { c["nbf"] = now.Unix() + 31 }},
		{"token_type_invalid", func(c map[string]any, _ *Cloudflare) { c["type"] = "org" }},
		{"service_identity_not_configured", func(_ map[string]any, v *Cloudflare) { v.CIClientID = "" }},
		{"service_client_id_mismatch", func(c map[string]any, _ *Cloudflare) { c["common_name"] = "private-service" }},
		{"service_token_type_invalid", func(c map[string]any, _ *Cloudflare) { delete(c, "type") }},
		{"service_user_identity_present", func(c map[string]any, _ *Cloudflare) { c["email"] = "private@example.com" }},
		{"user_identity_or_role_invalid", func(c map[string]any, _ *Cloudflare) { delete(c, "common_name") }},
		{"claims_json_invalid", func(c map[string]any, _ *Cloudflare) { c["aud"] = 42 }},
		{"", func(_ map[string]any, _ *Cloudflare) {}},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			v := &Cloudflare{Issuer: "issuer", Audience: "app", CIClientID: testCIClientID, keys: map[string]*rsa.PublicKey{"one": &key.PublicKey}, fetched: now, now: func() time.Time { return now }}
			claims := map[string]any{"iss": "issuer", "aud": []string{"app"}, "common_name": testCIClientID, "type": "app", "iat": now.Unix(), "exp": now.Unix() + 300}
			tc.change(claims, v)
			payload, _ := json.Marshal(claims)
			message := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"one"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
			digest := sha256.Sum256([]byte(message))
			token := message + "." + signTestDigest(t, key, digest[:])
			reason := ""
			calls := 0
			r := httptest.NewRequest("GET", "/v1/auth/session", nil)
			r = r.WithContext(WithAuthFailureObserver(r.Context(), func(s string) { reason = s; calls++ }))
			r.Header.Set("Cf-Access-Jwt-Assertion", token)
			_, ok := v.Authenticate(r)
			if ok != (tc.reason == "") || reason != tc.reason || (tc.reason != "" && calls != 1) {
				t.Fatalf("ok=%v reason=%q calls=%d", ok, reason, calls)
			}
		})
	}
}

func TestJWKSFailureClassificationAndRetry(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
		err              error
	}{
		{name: "TLS", err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, want: "jwks_tls_verification_failed"},
		{name: "DNS", err: &net.DNSError{Err: "private-host", Name: "private-host"}, want: "jwks_dns_failed"},
		{name: "timeout", err: context.DeadlineExceeded, want: "jwks_timeout"},
		{name: "canceled", err: context.Canceled, want: "jwks_request_canceled"},
		{name: "transport", err: errors.New("secret-upstream-detail"), want: "jwks_transport_failed"},
		{name: "HTTP", status: 403, body: "secret-response", want: "jwks_http_403"},
		{name: "JSON", status: 200, body: "secret-response", want: "jwks_json_invalid"},
		{name: "keys", status: 200, body: `{"keys":[]}`, want: "jwks_keys_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			calls := 0
			v := &Cloudflare{Issuer: "https://issuer.example", now: func() time.Time { return now }}
			v.Client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				calls++
				if tc.err != nil {
					return nil, &url.Error{Op: "Get", URL: "private-url", Err: tc.err}
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			assertJWKSFailure(t, v, tc.want)
			assertJWKSFailure(t, v, tc.want)
			if calls != 1 {
				t.Fatal("retry throttle changed", calls)
			}
			now = now.Add(time.Minute)
			assertJWKSFailure(t, v, tc.want)
			if calls != 2 {
				t.Fatal("retry did not resume", calls)
			}
		})
	}
}

func TestMalformedAssertionDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		values []string
		want   string
	}{
		{nil, "assertion_missing"}, {[]string{"a", "b"}, "assertion_duplicate"},
		{[]string{strings.Repeat("x", 16385)}, "assertion_oversized"},
		{[]string{"secret"}, "token_format_invalid"}, {[]string{"bad.payload.signature"}, "token_header_invalid"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/v1/auth/session", nil)
			r.Header["Cf-Access-Jwt-Assertion"] = tc.values
			reason := ""
			r = r.WithContext(WithAuthFailureObserver(r.Context(), func(s string) { reason = s }))
			if _, ok := (&Cloudflare{}).Authenticate(r); ok || reason != tc.want {
				t.Fatal(ok, reason)
			}
		})
	}
}

func assertJWKSFailure(t *testing.T, v *Cloudflare, want string) {
	t.Helper()
	key, reason := v.key(context.Background(), "private-kid")
	if key != nil || reason != want {
		t.Fatalf("key=%v reason=%q, want %q", key, reason, want)
	}
}
