// Package members manages invitations without granting privileged roles.
package members

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

type Config struct {
	Enabled                   bool
	AccountID, GroupID, Token string
}

// LoadConfig is called only by the API, never migrations, engine or workers.
func LoadConfig() (Config, error) {
	flag := os.Getenv("BATTLEFIELD_MANAGED_PLAYERS")
	if flag != "" && flag != "true" && flag != "false" {
		return Config{}, fmt.Errorf("invalid BATTLEFIELD_MANAGED_PLAYERS boolean")
	}
	c := Config{Enabled: flag == "true"}
	if !c.Enabled {
		return c, nil
	}
	c.AccountID = os.Getenv("BATTLEFIELD_MEMBERS_ACCOUNT_ID")
	c.GroupID = os.Getenv("BATTLEFIELD_MEMBERS_GROUP_ID")
	c.Token = os.Getenv("BATTLEFIELD_MEMBERS_API_TOKEN")
	if os.Getenv("BATTLEFIELD_AUTH_MODE") != "cloudflare" || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(c.AccountID) || !regexp.MustCompile(`^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$`).MatchString(c.GroupID) || strings.TrimSpace(c.Token) == "" || strings.ContainsAny(c.Token, "\r\n") {
		return Config{}, fmt.Errorf("managed Players require Cloudflare authentication, account ID, group UUID and API token")
	}
	return c, nil
}
