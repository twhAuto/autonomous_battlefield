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
	"strings"
	"sync"
	"time"
)

// Cloudflare verifies Access application JWTs against a fixed issuer's public keys.
// Keys expire after an hour; refresh attempts (including unknown kids) are bounded.
type Cloudflare struct {
	Issuer, Audience   string
	CIClientID         string
	Roles              map[string]Role
	Client             *http.Client
	mu                 sync.Mutex
	keys               map[string]*rsa.PublicKey
	fetched, attempted time.Time
	now                func() time.Time
}

func (c *Cloudflare) key(ctx context.Context, kid string) *rsa.PublicKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	if now.Sub(c.fetched) < time.Hour && c.keys[kid] != nil {
		return c.keys[kid]
	}
	if now.Sub(c.attempted) < 30*time.Second {
		return nil
	}
	c.attempted = now
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Issuer+"/cdn-cgi/access/certs", nil)
	if err != nil {
		return nil
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("JWKS redirects refused") }}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var document struct{ Keys []signingKey }
	if json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&document) != nil {
		return nil
	}
	keys := parseSigningKeys(document.Keys)
	if len(keys) == 0 {
		return nil
	}
	c.keys, c.fetched = keys, now
	return c.keys[kid]
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
	values := r.Header.Values("Cf-Access-Jwt-Assertion")
	if len(values) != 1 || len(values[0]) > 16384 {
		return Actor{}, false
	}
	parts := strings.Split(values[0], ".")
	if len(parts) != 3 {
		return Actor{}, false
	}
	var header struct{ Alg, Kid string }
	if json.Unmarshal(decode(parts[0]), &header) != nil || header.Alg != "RS256" || header.Kid == "" {
		return Actor{}, false
	}
	key := c.key(r.Context(), header.Kid)
	if key == nil {
		return Actor{}, false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], decode(parts[2])) != nil {
		return Actor{}, false
	}
	var claims accessClaims
	if json.Unmarshal(decode(parts[1]), &claims) != nil {
		return Actor{}, false
	}
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	audience := false
	for _, a := range claims.Aud {
		audience = audience || a == c.Audience
	}
	if claims.Iss != c.Issuer || !audience || claims.Exp <= now.Unix() || claims.Iat <= 0 || claims.Iat > now.Unix()+30 || claims.Nbf > now.Unix()+30 || (claims.Type != "" && claims.Type != "app") {
		return Actor{}, false
	}
	return c.actor(claims)
}

type accessClaims struct {
	Iss, Sub, Email, Type string
	CommonName            string `json:"common_name"`
	Aud                   []string
	Exp, Iat, Nbf         int64
}

// Service identities are explicitly enrolled and never inherit a human role.
func (c *Cloudflare) actor(claims accessClaims) (Actor, bool) {
	if claims.CommonName != "" {
		if c.CIClientID == "" || claims.CommonName != c.CIClientID || claims.Type != "app" || claims.Sub != "" || claims.Email != "" {
			return Actor{}, false
		}
		return Actor{ID: c.Issuer + "|service:" + claims.CommonName, Role: CI}, true
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	role := c.Roles[email]
	if claims.Sub == "" || email == "" || (role != Player && role != Developer && role != Admin) {
		return Actor{}, false
	}
	return Actor{ID: c.Issuer + "|" + claims.Sub, Email: email, Role: role}, true
}
