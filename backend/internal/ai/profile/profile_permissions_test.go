package profile

import (
	"context"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
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
		{"player default CLI", authn.Player, "cli", nil, false},
		{"player approved CLI", authn.Player, "cli", []string{"player"}, true},
		{"player revoked CLI", authn.Player, "cli", []string{"admin", "tester"}, false},
		{"tester default CLI", authn.Tester, "cli", nil, true},
		{"admin default API", authn.Admin, "api", nil, true},
		{"tester excluded", authn.Tester, "api", []string{"admin"}, false},
		{"unknown role", "root", "api", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.role != "" {
				ctx = authn.WithActor(ctx, authn.Actor{ID: "friend", Role: tt.role})
			}
			if got := Allowed(ctx, ai.ModelProfile{Runtime: tt.runtime, AllowedRoles: tt.roles}); got != tt.want {
				t.Fatalf("eligibility=%v want %v", got, tt.want)
			}
		})
	}
}
