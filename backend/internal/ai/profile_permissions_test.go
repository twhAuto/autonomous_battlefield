package ai

import (
	"context"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"testing"
)

func TestControllerEligibilityByIdentity(t *testing.T) {
	for _, tt := range []struct {
		name    string
		role    authn.Role
		runtime string
		roles   []string
		want    bool
	}{
		{"anonymous", "", "api", []string{"player"}, false},
		{"player unapproved API", authn.Player, "api", nil, false},
		{"player approved API", authn.Player, "api", []string{"player"}, true},
		{"player CLI even if allowlisted", authn.Player, "cli", []string{"player"}, false},
		{"developer default CLI", authn.Developer, "cli", nil, true},
		{"admin default API", authn.Admin, "api", nil, true},
		{"developer excluded", authn.Developer, "api", []string{"admin"}, false},
		{"unknown role", "root", "api", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.role != "" {
				ctx = authn.WithActor(ctx, authn.Actor{ID: "friend", Role: tt.role})
			}
			if got := ProfileAllowed(ctx, ModelProfile{Runtime: tt.runtime, AllowedRoles: tt.roles}); got != tt.want {
				t.Fatalf("eligibility=%v want %v", got, tt.want)
			}
		})
	}
}
