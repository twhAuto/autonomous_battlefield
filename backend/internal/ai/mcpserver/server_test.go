// Exercises MCP tool authorization and request scoping, and malformed-input fuzzing.

package mcpserver

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/capability"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

type fakeRepository struct {
	record          ai.DecisionRecord
	submitted       ai.PlanSubmission
	failures        int
	usage           int
	claimErr        error
	claimedProfiles []string
	claimFn         func([]string) (ai.DecisionRecord, error)
	err             error
	failErr         error
	usageErr        error
	noDeadline      bool
}

func (r *fakeRepository) CreateOrReuseDecision(context.Context, ai.CreateDecisionParams) (ai.DecisionRecord, error) {
	return r.record, nil
}
func (r *fakeRepository) GetDecision(context.Context, string) (ai.DecisionRecord, error) {
	return r.record, r.err
}
func (r *fakeRepository) ListWindowDecisions(context.Context, string, int, uint64) ([]ai.DecisionRecord, error) {
	return []ai.DecisionRecord{r.record}, nil
}
func (r *fakeRepository) SubmitDecisionPlan(_ context.Context, value ai.PlanSubmission) (ai.SubmissionResult, error) {
	r.submitted = value
	return ai.SubmissionResult{Accepted: true, Status: "accepted"}, r.err
}
func (r *fakeRepository) ResolveDecisionFallback(context.Context, string, int64, controller.PlanProposal, string, string) (bool, error) {
	return true, nil
}
func (r *fakeRepository) ClaimModelDecision(_ context.Context, _ string, profiles []string, _ time.Duration) (ai.DecisionRecord, error) {
	r.claimedProfiles = append([]string(nil), profiles...)
	record, err := r.record, r.claimErr
	if r.claimFn != nil {
		record, err = r.claimFn(profiles)
	}
	if !r.noDeadline {
		expiry := time.Now().Add(time.Minute)
		record.LeaseExpiresAt = &expiry
	}
	return record, err
}
func (r *fakeRepository) FinishDecisionFailure(context.Context, string, string, ai.Usage, map[string]any) error {
	r.failures++
	return r.failErr
}
func (r *fakeRepository) RecordDecisionUsage(context.Context, string, ai.Usage, map[string]any) error {
	r.usage++
	return r.usageErr
}

func TestStreamableMCPToolsAreAuthenticatedAndScoped(t *testing.T) {
	repository := &fakeRepository{record: ai.DecisionRecord{ID: "request-1", Version: 1, TargetTick: 10, Observation: ai.DecisionContext{RequestID: "request-1", TargetTick: 10, ExpectedVersion: 1, Legal: ai.LegalChoices{Upgrades: []game.Upgrade{{ID: "plating", Cost: 20}}, ActionKinds: []string{"attack", "defend"}, Units: []game.UnitType{{ID: "ranger"}}}}}}
	var logs bytes.Buffer
	service := NewWithLogger(repository, "secret", []string{"https://agent.example"}, slog.New(slog.NewJSONHandler(&logs, nil)))
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverContext := context.WithValue(context.Background(), requestIDKey{}, "request-1")
	serverSession, err := service.mcpServer().Connect(serverContext, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	defer serverSession.Close()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 3 {
		t.Fatalf("tools=%#v err=%v", tools, err)
	}
	contextResult, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_decision_context", Arguments: map[string]any{"requestId": "request-1"}})
	if err != nil || contextResult.IsError {
		t.Fatalf("context=%#v err=%v", contextResult, err)
	}
	if len(contextResult.Content) == 0 {
		t.Fatal("missing text context")
	}
	text, ok := contextResult.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, `"targetTick":10`) || !strings.Contains(text.Text, `"expectedVersion":1`) {
		t.Fatal("explicit decision metadata missing")
	}
	denied, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_decision_context", Arguments: map[string]any{"requestId": "another"}})
	if err != nil || !denied.IsError {
		t.Fatalf("cross-request result=%#v err=%v", denied, err)
	}
	submitted, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "submit_strategy_plan", Arguments: map[string]any{"requestId": "request-1", "idempotencyKey": "once", "expectedVersion": 1, "targetTick": 10, "name": "advance", "rationale": "The visible lane is open, so reinforce the advance.", "actionWeights": map[string]any{"attack": 1200}, "reserveGold": 0, "upgradeOrders": []any{"plating"}, "spendOrder": []any{"upgrades", "recruitment"}, "constructionPriorities": []any{}, "recruitmentPriorities": []any{}, "recruitmentOrders": []any{map[string]any{"unitType": "ranger", "count": 2}}}})
	if err != nil || submitted.IsError || repository.submitted.IdempotencyKey != "once" || repository.submitted.Plan.Rationale == "" || len(repository.submitted.Plan.UpgradeOrders) != 1 || repository.submitted.Plan.UpgradeOrders[0] != "plating" || repository.submitted.Plan.RecruitmentOrders == nil || (*repository.submitted.Plan.RecruitmentOrders)[0].Count != 2 {
		t.Fatalf("submit=%#v err=%v", submitted, err)
	}

	repository.record.Observation.Legal.Buildings = []game.BuildingType{{ID: "defense_tower"}}
	repository.record.Observation.Legal.EconomySites = []controller.Coordinate{{X: 9, Y: 7}}
	repository.record.Observation.Legal.RecruitmentSources = []string{"a:barracks:1"}
	expanded, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "submit_strategy_plan", Arguments: map[string]any{
		"requestId": "request-1", "idempotencyKey": "expanded", "expectedVersion": 1, "targetTick": 10, "name": "fortify", "rationale": "Protect the forward recruitment site.", "actionWeights": map[string]any{"defend": 1200}, "reserveGold": 0, "spendOrder": []string{"construction", "recruitment"}, "constructionPriorities": []any{}, "recruitmentPriorities": []any{},
		"constructionOrders": []any{map[string]any{"buildingType": "defense_tower", "position": map[string]any{"x": 9, "y": 7}}},
		"recruitmentOrders":  []any{map[string]any{"unitType": "ranger", "count": 1, "sourceBuildingId": "a:barracks:1"}},
	}})
	if err != nil || expanded.IsError || repository.submitted.IdempotencyKey != "expanded" || repository.submitted.Plan.ConstructionOrders == nil || (*repository.submitted.Plan.ConstructionOrders)[0].BuildingType != "defense_tower" || (*repository.submitted.Plan.RecruitmentOrders)[0].SourceBuildingID != "a:barracks:1" {
		t.Fatalf("expanded tool submission: %+v %v", expanded, err)
	}
	assertDecisionToolLogs(t, logs.String())
}

func assertDecisionToolLogs(t *testing.T, logs string) {
	t.Helper()
	for _, expected := range []string{`"msg":"decision_context_served"`, `"msg":"decision_context_denied"`, `"msg":"decision_plan_submitted"`, `"accepted":true`} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("logs do not contain %s: %s", expected, logs)
		}
	}
}

func TestMCPRejectsMissingCapabilityAndForeignOrigin(t *testing.T) {
	handler := New(&fakeRepository{}, "secret", []string{"https://allowed.example"}).Handler()
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodPost, "http://server", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatal(missing.Code)
	}
	token, _ := capability.MintCapability("secret", "request", time.Now().Add(time.Minute))
	foreignRequest := httptest.NewRequest(http.MethodPost, "http://server", nil)
	foreignRequest.Header.Set("Authorization", "Bearer "+token)
	foreignRequest.Header.Set("Origin", "https://evil.example")
	foreign := httptest.NewRecorder()
	handler.ServeHTTP(foreign, foreignRequest)
	if foreign.Code != http.StatusForbidden {
		t.Fatal(foreign.Code)
	}
	largeRequest := httptest.NewRequest(http.MethodPost, "http://server", strings.NewReader(strings.Repeat("x", 65<<10)))
	largeRequest.Header.Set("Authorization", "Bearer "+token)
	largeRequest.Header.Set("Content-Type", "application/json")
	largeRequest.Header.Set("Accept", "application/json, text/event-stream")
	large := httptest.NewRecorder()
	handler.ServeHTTP(large, largeRequest)
	if large.Code != http.StatusRequestEntityTooLarge {
		t.Fatal(large.Code)
	}
}

func FuzzMCPRequestBody(f *testing.F) {
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":"tools/call"}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 64<<10 {
			t.Skip()
		}
		token, err := capability.MintCapability("secret", "request", time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "http://server", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		response := httptest.NewRecorder()
		New(&fakeRepository{}, "secret", nil).Handler().ServeHTTP(response, request)
		if response.Body.Len() > 1<<20 {
			t.Fatalf("response body exceeded safety bound: %d bytes", response.Body.Len())
		}
	})
}

func (r *fakeRepository) ListFactionMemory(context.Context, string, int, string, uint64) ([]ai.FactionMemoryEntry, error) {
	return nil, nil
}
