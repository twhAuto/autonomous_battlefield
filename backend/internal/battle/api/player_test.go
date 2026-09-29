package api

import (
	"context"
	"encoding/json"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlayerProjectionAfterSharedCachedRead(t *testing.T) {
	secret := "internal-secret-diagnostic"
	run := &battleservice.BattleRun{ID: "battle", OwnerID: "owner", Status: "failed", ErrorMessage: &secret, ErrorCode: &secret, ScenarioDigest: secret, ScenarioSnapshot: game.Scenario{Factions: []game.FactionSetup{{ID: "a", Name: "A", Controller: game.ControllerConfig{Type: "model", ProfileID: secret}}}}}
	player := httptest.NewRequest("GET", "/v1/battles/battle", nil).WithContext(authn.WithActor(context.Background(), authn.Actor{ID: "friend", Role: authn.Player}))
	raw, e := json.Marshal(battleResponseFor(player, run))
	if e != nil || strings.Contains(string(raw), secret) {
		t.Fatal(string(raw), e)
	}
	dev := player.WithContext(authn.WithActor(context.Background(), authn.Actor{ID: "dev", Role: authn.Developer}))
	raw, e = json.Marshal(battleResponseFor(dev, run))
	if e != nil || !strings.Contains(string(raw), secret) {
		t.Fatal("shared cache was mutated", string(raw), e)
	}
	profiles := []ai.ModelProfile{{ID: "cli", Runtime: "cli", Available: true}, {ID: "paid", Runtime: "api", Available: true}, {ID: "approved", Runtime: "api", Available: true, AllowedRoles: []string{"player"}, UnavailableReason: secret}}
	raw, e = json.Marshal(visibleProfiles(player, profiles))
	if e != nil || strings.Contains(string(raw), secret) || strings.Contains(string(raw), "paid") || strings.Contains(string(raw), "cli") || !strings.Contains(string(raw), "approved") {
		t.Fatal(string(raw), e)
	}
}

func TestPlayerCatalogIncludesOnlyApprovedAvailableCLI(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/models", nil).WithContext(authn.WithActor(context.Background(), authn.Actor{ID: "friend", Role: authn.Player}))
	profiles := []ai.ModelProfile{
		{ID: "approved-cli", Runtime: "cli", Available: true, AllowedRoles: []string{"player"}},
		{ID: "unapproved-cli", Runtime: "cli", Available: true},
		{ID: "offline-cli", Runtime: "cli", Available: false, AllowedRoles: []string{"player"}},
	}
	raw, err := json.Marshal(visibleProfiles(r, profiles))
	if err != nil || !strings.Contains(string(raw), "approved-cli") || strings.Contains(string(raw), "unapproved-cli") || strings.Contains(string(raw), "offline-cli") {
		t.Fatalf("catalog: %s %v", raw, err)
	}
}

func TestBattleResponsesExposeDecisionInterval(t *testing.T) {
	run := &battleservice.BattleRun{ID: "battle", ScenarioSnapshot: game.Scenario{DecisionIntervalTicks: 10}}
	for _, role := range []authn.Role{authn.Player, authn.Developer} {
		request := httptest.NewRequest("GET", "/v1/battles/battle", nil).WithContext(authn.WithActor(context.Background(), authn.Actor{ID: "caller", Role: role}))
		raw, err := json.Marshal(battleResponseFor(request, run))
		if err != nil || !strings.Contains(string(raw), `"decisionIntervalTicks":10`) {
			t.Fatalf("%s response: %s %v", role, raw, err)
		}
	}
}
