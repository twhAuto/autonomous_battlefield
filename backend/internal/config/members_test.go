// Checks managed Player settings require Cloudflare authentication and well-formed
// Cloudflare identifiers.

package config

import (
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
)

func TestMembersConfig(t *testing.T) {
	for _, key := range []string{envManagedPlayers, envMembersAccountID, envMembersGroupID, envMembersAPIToken} {
		t.Setenv(key, "")
	}
	cloudflare := authn.Config{Mode: authn.ModeCloudflare}
	if c, err := parseMembersConfig(cloudflare); err != nil || c.Enabled {
		t.Fatal(c, err)
	}
	t.Setenv(envManagedPlayers, "true")
	if _, err := parseMembersConfig(cloudflare); err == nil {
		t.Fatal("incomplete settings accepted")
	}
	t.Setenv(envMembersAccountID, strings.Repeat("a", 32))
	t.Setenv(envMembersGroupID, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	t.Setenv(envMembersAPIToken, "token")
	if c, err := parseMembersConfig(cloudflare); err != nil || !c.Enabled || c.AccountID != strings.Repeat("a", 32) || c.Token != "token" {
		t.Fatal(c, err)
	}
	// The mode comes from the validated auth configuration, not the raw environment.
	t.Setenv(envAuthMode, "cloudflare")
	if _, err := parseMembersConfig(authn.Config{Mode: localPasswordMode}); err == nil {
		t.Fatal("managed Players accepted without Cloudflare authentication")
	}
	for _, tt := range []struct{ key, value string }{{envManagedPlayers, "yes"}, {envMembersAccountID, "../x"}, {envMembersGroupID, "../x"}, {envMembersAPIToken, " "}, {envMembersAPIToken, "x\ny"}} {
		t.Run(tt.key+tt.value, func(t *testing.T) {
			t.Setenv(tt.key, tt.value)
			if _, err := parseMembersConfig(cloudflare); err == nil {
				t.Fatal("unsafe settings accepted")
			}
		})
	}
}

func TestLoadAPIParsesMembers(t *testing.T) {
	path := writeConfigFile(t, validHTTP)
	t.Setenv(envManagedPlayers, "true")
	if _, err := LoadAPI(path); err == nil || !strings.Contains(err.Error(), "managed Players require Cloudflare") {
		t.Fatalf("LoadAPI() error = %v, want the managed Players requirement", err)
	}
}
