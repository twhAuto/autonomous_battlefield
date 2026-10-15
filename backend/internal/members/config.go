// Package members manages invitations without granting privileged roles.
package members

// Config holds the Cloudflare credentials for the dedicated Player Access group. The
// config package loads and validates them.
type Config struct {
	AccountID, GroupID, Token string
}
