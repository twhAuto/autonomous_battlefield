package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

func TestPlayerProjectionAfterSharedCachedRead(t *testing.T) {
	secret := "internal-secret-diagnostic"
	model := game.ControllerConfig{Type: "model", ProfileID: "frontier", Provider: "openai", Runtime: "api", ModelIdentifier: "gpt-test", ReasoningEffort: "high"}
	league := &battleservice.BattleLeague{ID: "league", Name: "Autumn Cup", FixtureID: "fixture", Stage: "final", Round: 1, Tie: 1}
	run := &battleservice.BattleRun{ID: "battle", OwnerID: "owner", Status: "failed", ErrorMessage: &secret, ErrorCode: &secret, ScenarioDigest: secret, League: league, ScenarioSnapshot: game.Scenario{Factions: []game.FactionSetup{{ID: "a", Name: "A", Controller: model}}}}
	player := httptest.NewRequest("GET", "/v1/battles/battle", nil).WithContext(authn.WithActor(context.Background(), authn.Actor{ID: "friend", Role: authn.Player}))
	guest := player.WithContext(authn.WithActor(context.Background(), authn.Actor{ID: "guest", Role: authn.Guest}))
	// Players and Guests see which model played each side and the battle's league, but
	// no diagnostics.
	for _, request := range []*http.Request{player, guest} {
		raw, e := json.Marshal(battleResponseFor(request, run))
		if e != nil || strings.Contains(string(raw), secret) || !strings.Contains(string(raw), `"modelIdentifier":"gpt-test"`) || !strings.Contains(string(raw), `"reasoningEffort":"high"`) || !strings.Contains(string(raw), `"name":"Autumn Cup"`) {
			t.Fatal(string(raw), e)
		}
	}
	tester := player.WithContext(authn.WithActor(context.Background(), authn.Actor{ID: "tester", Role: authn.Tester}))
	raw, e := json.Marshal(battleResponseFor(tester, run))
	if e != nil || !strings.Contains(string(raw), secret) {
		t.Fatal("shared cache was mutated", string(raw), e)
	}
	profiles := []ai.ModelProfile{{ID: "cli", Runtime: "cli", Available: true}, {ID: "paid", Runtime: "api", Available: true}, {ID: "approved", Runtime: "api", Available: true, AllowedRoles: []string{"player"}, UnavailableReason: secret}}
	raw, e = json.Marshal(visibleProfiles(player, profiles))
	if e != nil || strings.Contains(string(raw), secret) || strings.Contains(string(raw), "paid") || strings.Contains(string(raw), "cli") || !strings.Contains(string(raw), "approved") {
		t.Fatal(string(raw), e)
	}
}

func TestPlayerDecisionHistoryKeepsPlansAndDropsDiagnostics(t *testing.T) {
	secret := "internal-secret-diagnostic"
	started, claimed := int64(42), time.Unix(1, 0)
	battles := &fakeBattleService{history: []battleservice.DecisionHistory{{
		RequestID: "request", FactionID: "faction", TargetTick: 10, Status: "fallback", ControllerType: "model",
		ResolvedPlan:    map[string]any{"name": "Hold the bridge", "rationale": "Protect the economy."},
		ContextDelivery: secret, CLIProcessStartMS: &started, ReasoningEffort: secret, ClaimedAt: &claimed, TraceURL: secret,
		ProfileID: secret, Provider: secret, Runtime: secret, ModelIdentifier: secret, FallbackSource: secret, ErrorCategory: secret,
		Usage: ai.Usage{Scope: secret, InputTokens: 4242},
	}}}
	router := httpapi.NewRouter(NewHandler(battles))
	read := func(role authn.Role) string {
		request := httptest.NewRequest("GET", "/v1/battles/7f22e693-bacd-46bb-b942-64fa613cd388/decisions?attempt=1", nil).WithContext(authn.WithActor(context.Background(), authn.Actor{ID: "viewer", Role: role}))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("%s: %d %s", role, response.Code, response.Body.String())
		}
		return response.Body.String()
	}
	for _, role := range []authn.Role{authn.Player, authn.Guest} {
		checkGameplayDecisions(t, role, read(role), secret)
	}
	if tester := read(authn.Tester); !strings.Contains(tester, secret) || !strings.Contains(tester, "4242") {
		t.Fatalf("tester response lost diagnostics: %s", tester)
	}
}

// checkGameplayDecisions requires a Player's or Guest's decision history to keep the
// plan and drop every diagnostic.
func checkGameplayDecisions(t *testing.T, role authn.Role, body, secret string) {
	t.Helper()
	for _, want := range []string{`"requestId":"request"`, `"factionId":"faction"`, `"targetTick":10`, `"status":"fallback"`, `"controllerType":"model"`, "Hold the bridge", "Protect the economy."} {
		if !strings.Contains(body, want) {
			t.Fatalf("%s response lacks %s: %s", role, want, body)
		}
	}
	for _, hidden := range []string{secret, "4242", "usage", "claimedAt", "cliProcessStartMs"} {
		if strings.Contains(body, hidden) {
			t.Fatalf("%s response exposes %s: %s", role, hidden, body)
		}
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

func TestBattleResponsesExposeNextProduction(t *testing.T) {
	scenario := game.Scenario{DecisionIntervalTicks: 10}
	cases := []struct {
		name string
		run  battleservice.BattleRun
		want string
	}{
		{name: "running", run: battleservice.BattleRun{Status: "running", RequestedTicks: 100, ScenarioSnapshot: scenario, CurrentState: game.State{Tick: 12}}, want: `"production":{"nextTick":20,"opening":false}`},
		{name: "opening pending", run: battleservice.BattleRun{Status: "queued", RequestedTicks: 100, ScenarioSnapshot: game.Scenario{DecisionIntervalTicks: 10, SnapshotVersion: game.DoctrineSnapshotVersion}}, want: `"production":{"nextTick":0,"opening":true}`},
		{name: "no window before limit", run: battleservice.BattleRun{Status: "running", RequestedTicks: 25, ScenarioSnapshot: scenario, CurrentState: game.State{Tick: 21}}, want: `"production":null`},
		{name: "settled", run: battleservice.BattleRun{Status: "completed", RequestedTicks: 100, ScenarioSnapshot: scenario, CurrentState: game.State{Tick: 12}}, want: `"production":null`},
	}
	for _, tt := range cases {
		for _, role := range []authn.Role{authn.Player, authn.Tester} {
			request := httptest.NewRequest("GET", "/v1/battles/battle", nil).WithContext(authn.WithActor(context.Background(), authn.Actor{ID: "caller", Role: role}))
			raw, err := json.Marshal(battleResponseFor(request, &tt.run))
			if err != nil || !strings.Contains(string(raw), tt.want) {
				t.Fatalf("%s as %s: %s %v", tt.name, role, raw, err)
			}
		}
	}
}

func TestBattleResponsesReportTheUnitCapAndContestedTerritoryOnlyWhenSet(t *testing.T) {
	run := &battleservice.BattleRun{ID: "battle", Status: "running", ScenarioSnapshot: game.Scenario{Ruleset: game.Ruleset{MaximumUnitCount: 40}}}
	for _, role := range []authn.Role{authn.Player, authn.Tester} {
		request := httptest.NewRequest("GET", "/v1/battles/battle", nil).WithContext(authn.WithActor(context.Background(), authn.Actor{ID: "viewer", Role: role}))
		read := func() map[string]any {
			raw, err := json.Marshal(battleResponseFor(request, run))
			var body map[string]any
			if err == nil {
				err = json.Unmarshal(raw, &body)
			}
			if err != nil {
				t.Fatal(err)
			}
			return body
		}
		run.ScenarioSnapshot.Ruleset.MaximumUnitCount = 40
		run.ScenarioSnapshot.Ruleset.Buildings.ContestedTerritory = true
		if body := read(); body["maximumUnits"] != 40.0 || body["contestedTerritory"] != true {
			t.Fatalf("%s maximumUnits = %v, contestedTerritory = %v", role, body["maximumUnits"], body["contestedTerritory"])
		}
		run.ScenarioSnapshot.Ruleset.MaximumUnitCount = 0
		run.ScenarioSnapshot.Ruleset.Buildings.ContestedTerritory = false
		body := read()
		if _, present := body["maximumUnits"]; present {
			t.Fatalf("%s reports a cap for an unlimited ruleset", role)
		}
		if _, present := body["contestedTerritory"]; present {
			t.Fatalf("%s reports contested territory for a ruleset without it", role)
		}
	}
}
