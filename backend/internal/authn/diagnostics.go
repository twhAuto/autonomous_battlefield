package authn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"time"
)

type authFailureObserverKey struct{}

// WithAuthFailureObserver connects authentication to server-side request logging.
// Reasons are fixed codes; credentials, claims and raw upstream errors are excluded.
func WithAuthFailureObserver(ctx context.Context, observe func(string)) context.Context {
	return context.WithValue(ctx, authFailureObserverKey{}, observe)
}

func reportAuthFailure(ctx context.Context, reason string) {
	if observe, ok := ctx.Value(authFailureObserverKey{}).(func(string)); ok && observe != nil {
		observe(reason)
	}
}

func keyFetchFailure(err error) string {
	var certificate *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var dns *net.DNSError
	var network net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return "jwks_request_canceled"
	case errors.As(err, &certificate), errors.As(err, &authority):
		return "jwks_tls_verification_failed"
	case errors.As(err, &dns):
		return "jwks_dns_failed"
	case errors.As(err, &network) && network.Timeout():
		return "jwks_timeout"
	default:
		return "jwks_transport_failed"
	}
}

func (c *Cloudflare) claimsFailure(claims accessClaims, now time.Time) string {
	audience := false
	for _, a := range claims.Aud {
		audience = audience || a == c.Audience
	}
	switch {
	case claims.Iss != c.Issuer:
		return "issuer_mismatch"
	case !audience:
		return "audience_mismatch"
	case claims.Exp <= now.Unix():
		return "token_expired"
	case claims.Iat <= 0:
		return "issued_at_missing_or_invalid"
	case claims.Iat > now.Unix()+30:
		return "issued_at_in_future"
	case claims.Nbf > now.Unix()+30:
		return "token_not_yet_valid"
	case claims.Type != "" && claims.Type != "app":
		return "token_type_invalid"
	default:
		return ""
	}
}

func (c *Cloudflare) identityFailure(claims accessClaims) string {
	if claims.CommonName == "" {
		return "user_identity_or_role_invalid"
	}
	switch {
	case c.CIClientID == "":
		return "service_identity_not_configured"
	case claims.CommonName != c.CIClientID:
		return "service_client_id_mismatch"
	case claims.Type != "app":
		return "service_token_type_invalid"
	default:
		return "service_user_identity_present"
	}
}
