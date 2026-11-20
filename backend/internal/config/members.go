// Parses and validates managed Player invitation settings.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
)

// Members configures managed Player invitations, which sync a dedicated Cloudflare
// Access group. Only the API server loads it.
type Members struct {
	Enabled   bool
	AccountID string
	GroupID   string
	Token     string
}

var (
	cloudflareAccountID = regexp.MustCompile(`^[a-f0-9]{32}$`)
	accessGroupID       = regexp.MustCompile(`^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)
)

// parseMembersConfig requires Cloudflare authentication, taken from the already
// validated auth configuration rather than a second read of the environment.
func parseMembersConfig(auth authn.Config) (Members, error) {
	flag := os.Getenv(envManagedPlayers)
	if flag != "" && flag != "true" && flag != "false" {
		return Members{}, fmt.Errorf("environment variable %q must be true or false", envManagedPlayers)
	}
	if flag != "true" {
		return Members{}, nil
	}
	members := Members{Enabled: true, AccountID: os.Getenv(envMembersAccountID), GroupID: os.Getenv(envMembersGroupID), Token: os.Getenv(envMembersAPIToken)}
	if auth.Mode != authn.ModeCloudflare || !cloudflareAccountID.MatchString(members.AccountID) || !accessGroupID.MatchString(members.GroupID) || strings.TrimSpace(members.Token) == "" || strings.ContainsAny(members.Token, "\r\n") {
		return Members{}, fmt.Errorf("managed Players require Cloudflare authentication, account ID, group UUID and API token")
	}
	return members, nil
}
