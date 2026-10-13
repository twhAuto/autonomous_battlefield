// Package capability issues and verifies request-scoped HMAC bearer tokens that let one
// model session call the AI gateway's tools for one decision until its lease ends.
package capability

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type capabilityClaims struct {
	RequestID string `json:"requestId"`
	ExpiresAt int64  `json:"expiresAt"`
}

// Mint signs a token granting access to one request until expiresAt.
// The token is a bearer credential and must stay out of logs and model context.
func Mint(secret, requestID string, expiresAt time.Time) (string, error) {
	if secret == "" || requestID == "" {
		return "", errors.New("capability secret and request ID are required")
	}
	payload, err := json.Marshal(capabilityClaims{RequestID: requestID, ExpiresAt: expiresAt.Unix()})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Verify checks the signature and expiry and returns the authorized
// request ID; callers must still check the request's current state.
func Verify(secret, token string, now time.Time) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", errors.New("invalid capability token")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("invalid capability token")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return "", errors.New("invalid capability token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", errors.New("invalid capability token")
	}
	var claims capabilityClaims
	if json.Unmarshal(payload, &claims) != nil || claims.RequestID == "" || now.Unix() >= claims.ExpiresAt {
		return "", errors.New("expired capability token")
	}
	return claims.RequestID, nil
}
