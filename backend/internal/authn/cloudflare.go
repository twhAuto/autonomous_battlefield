package authn

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cloudflare verifies Access application JWTs against a fixed issuer's public keys.
// Keys expire after an hour; refresh attempts (including unknown kids) are bounded.
type Cloudflare struct {
	ResolvePlayer      func(context.Context, string) bool
	Issuer, Audience   string
	CIClientID         string
	Roles              map[string]Role
	Client             *http.Client
	mu                 sync.Mutex
	keys               map[string]*rsa.PublicKey
	fetched, attempted time.Time
	keyFailure         string
	now                func() time.Time
}

func (c *Cloudflare) key(ctx context.Context, kid string) (*rsa.PublicKey, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	if now.Sub(c.fetched) < time.Hour && c.keys[kid] != nil {
		return c.keys[kid], ""
	}
	if now.Sub(c.attempted) < 30*time.Second {
		if c.keyFailure != "" {
			return nil, c.keyFailure
		}
		return nil, "jwks_refresh_throttled"
	}
	c.attempted = now
	fail := func(reason string) (*rsa.PublicKey, string) { c.keyFailure = reason; return nil, reason }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Issuer+"/cdn-cgi/access/certs", nil)
	if err != nil {
		return fail("jwks_request_invalid")
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("JWKS redirects refused") }}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fail(keyFetchFailure(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail("jwks_http_" + strconv.Itoa(resp.StatusCode))
	}
	var document struct{ Keys []signingKey }
	if json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&document) != nil {
		return fail("jwks_json_invalid")
	}
	keys := parseSigningKeys(document.Keys)
	if len(keys) == 0 {
		return fail("jwks_keys_invalid")
	}
	c.keys, c.fetched = keys, now
	c.keyFailure = ""
	if c.keys[kid] == nil {
		return nil, "jwks_kid_unknown"
	}
	return c.keys[kid], ""
}

type signingKey struct{ Kid, Kty, Alg, Use, N, E string }

func parseSigningKeys(documentKeys []signingKey) map[string]*rsa.PublicKey {
	keys := make(map[string]*rsa.PublicKey)
	for _, j := range documentKeys {
		if j.Kty != "RSA" || j.Kid == "" || (j.Alg != "" && j.Alg != "RS256") || (j.Use != "" && j.Use != "sig") {
			continue
		}
		n, e := decode(j.N), decode(j.E)
		if len(n) < 256 || len(e) == 0 || len(e) > 4 {
			continue
		}
		exponent := new(big.Int).SetBytes(e).Int64()
		if exponent < 3 || exponent > 2147483647 || exponent%2 == 0 {
			continue
		}
		if keys[j.Kid] != nil {
			return nil
		}
		keys[j.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exponent)}
	}
	return keys
}

func decode(s string) []byte { b, _ := base64.RawURLEncoding.DecodeString(s); return b }
func (c *Cloudflare) Authenticate(r *http.Request) (Actor, bool) {
	reject := func(reason string) (Actor, bool) { reportAuthFailure(r.Context(), reason); return Actor{}, false }
	values := r.Header.Values("Cf-Access-Jwt-Assertion")
	if len(values) == 0 {
		return reject("assertion_missing")
	}
	if len(values) != 1 {
		return reject("assertion_duplicate")
	}
	if len(values[0]) > 16384 {
		return reject("assertion_oversized")
	}
	parts := strings.Split(values[0], ".")
	if len(parts) != 3 {
		return reject("token_format_invalid")
	}
	var header struct{ Alg, Kid string }
	if json.Unmarshal(decode(parts[0]), &header) != nil || header.Alg != "RS256" || header.Kid == "" {
		return reject("token_header_invalid")
	}
	key, reason := c.key(r.Context(), header.Kid)
	if key == nil {
		return reject(reason)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], decode(parts[2])) != nil {
		return reject("signature_invalid")
	}
	var claims accessClaims
	if json.Unmarshal(decode(parts[1]), &claims) != nil {
		return reject("claims_json_invalid")
	}
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	if reason := c.claimsFailure(claims, now); reason != "" {
		return reject(reason)
	}
	actor, ok := c.actorWithContext(r.Context(), claims)
	if !ok {
		return reject(c.identityFailure(claims))
	}
	return actor, true
}

type accessClaims struct {
	Iss, Sub, Email, Type string
	CommonName            string `json:"common_name"`
	Aud                   accessAudience
	Exp, Iat, Nbf         int64
}

// Service identities are explicitly enrolled and never inherit a human role.
func (c *Cloudflare) actor(claims accessClaims) (Actor, bool) {
	return c.actorWithContext(context.Background(), claims)
}
func (c *Cloudflare) actorWithContext(ctx context.Context, claims accessClaims) (Actor, bool) {
	if claims.CommonName != "" {
		if c.CIClientID == "" || claims.CommonName != c.CIClientID || claims.Type != "app" || claims.Sub != "" || claims.Email != "" {
			return Actor{}, false
		}
		return Actor{ID: c.Issuer + "|service:" + claims.CommonName, Role: CI}, true
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	role := c.Roles[email]
	if claims.Sub != "" && email != "" && c.ResolvePlayer != nil && role != Admin && role != Developer {
		role = ""
		if c.ResolvePlayer(ctx, email) {
			role = Player
		}
	}
	if claims.Sub == "" || email == "" || (role != Player && role != Developer && role != Admin) {
		return Actor{}, false
	}
	return Actor{ID: c.Issuer + "|" + claims.Sub, Email: email, Role: role}, true
}
