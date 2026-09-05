package mcpserver

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
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
	if r.claimFn != nil {
		return r.claimFn(profiles)
	}
	return r.record, r.claimErr
}
func (r *fakeRepository) FinishDecisionFailure(context.Context, string, string, ai.Usage, map[string]any) error {
	r.failures++
	return nil
}
func (r *fakeRepository) RecordDecisionUsage(context.Context, string, ai.Usage, map[string]any) error {
	r.usage++
	return nil
}

func TestStreamableMCPToolsAreAuthenticatedAndScoped(t *testing.T) {
	repository := &fakeRepository{record: ai.DecisionRecord{ID: "request-1", Version: 1, TargetTick: 10, Observation: ai.DecisionContext{RequestID: "request-1", TargetTick: 10, ExpectedVersion: 1, Legal: ai.LegalChoices{ActionKinds: []string{"attack", "defend"}}}}}
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
	if err != nil || len(tools.Tools) != 2 {
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
	if !ok || !strings.Contains(text.Text, "targetTick=10") || !strings.Contains(text.Text, "expectedVersion=1") {
		t.Fatal("explicit decision metadata missing")
	}
	denied, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_decision_context", Arguments: map[string]any{"requestId": "another"}})
	if err != nil || !denied.IsError {
		t.Fatalf("cross-request result=%#v err=%v", denied, err)
	}
	submitted, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "submit_strategy_plan", Arguments: map[string]any{"requestId": "request-1", "idempotencyKey": "once", "expectedVersion": 1, "targetTick": 10, "name": "advance", "rationale": "The visible lane is open, so reinforce the advance.", "actionWeights": map[string]any{"attack": 1200}, "reserveGold": 0, "spendOrder": []any{"recruitment"}, "constructionPriorities": []any{}, "recruitmentPriorities": []any{}}})
	if err != nil || submitted.IsError || repository.submitted.IdempotencyKey != "once" || repository.submitted.Plan.Rationale == "" {
		t.Fatalf("submit=%#v err=%v", submitted, err)
	}
	for _, expected := range []string{`"msg":"decision_context_served"`, `"msg":"decision_context_denied"`, `"msg":"decision_plan_submitted"`, `"accepted":true`} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatalf("logs do not contain %s: %s", expected, logs.String())
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
	token, _ := ai.MintCapability("secret", "request", time.Now().Add(time.Minute))
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
		token, err := ai.MintCapability("secret", "request", time.Now().Add(time.Minute))
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

func TestAgentControlPlaneAuthorizationAndEmptyQueue(t *testing.T) {
	repository := &fakeRepository{claimErr: ai.ErrNoDecision}
	handler := New(repository, "secret", nil).AgentHandler(repository)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/claim", strings.NewReader(`{}`)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatal(unauthorized.Code)
	}
	request := httptest.NewRequest(http.MethodPost, "/claim", strings.NewReader(`{"workerId":"agent","leaseSeconds":1,"profileIds":["profile"]}`))
	request.Header.Set("Authorization", "Bearer secret")
	empty := httptest.NewRecorder()
	handler.ServeHTTP(empty, request)
	if empty.Code != http.StatusNoContent {
		t.Fatal(empty.Code)
	}
}

func TestAgentControlPlaneRejectsMalformedBodies(t *testing.T) {
	handler := New(&fakeRepository{}, "secret", nil).AgentHandler(&fakeRepository{})
	for _, path := range []string{"/claim", "/fail", "/usage"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("bad"))
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s=%d", path, response.Code)
		}
	}
}

func TestAgentControlPlane(t *testing.T) {
	repository := &fakeRepository{record: ai.DecisionRecord{ID: "request", ProfileID: "profile", Status: "accepted"}}
	var logs bytes.Buffer
	handler := NewWithLogger(repository, "secret", nil, slog.New(slog.NewJSONHandler(&logs, nil))).AgentHandler(repository)
	claim := httptest.NewRequest(http.MethodPost, "/claim", strings.NewReader(`{"workerId":"agent","leaseSeconds":5,"profileIds":["profile","profile","  "]}`))
	claim.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, claim)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code)
	}
	for path, body := range map[string]string{"/fail": `{"requestId":"request","category":"provider","metadata":{"runnerOutcome":"deadline_exceeded"}}`, "/usage": `{"requestId":"request","usage":{"requests":1}}`} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer secret")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("%s=%d", path, recorder.Code)
		}
	}
	if repository.failures != 1 || repository.usage != 1 {
		t.Fatalf("repo=%#v", repository)
	}
	if len(repository.claimedProfiles) != 1 || repository.claimedProfiles[0] != "profile" {
		t.Fatalf("claimed profiles=%v", repository.claimedProfiles)
	}
	for _, expected := range []string{`"msg":"decision_claimed"`, `"msg":"decision_runner_failed"`, `"runner_outcome":"deadline_exceeded"`, `"msg":"decision_usage_recorded"`} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatalf("logs do not contain %s: %s", expected, logs.String())
		}
	}
}

func TestAgentClaimLongPollWakesWhenDecisionArrives(t *testing.T) {
	var available atomic.Bool
	firstClaim := make(chan struct{})
	var calls atomic.Int32
	repository := &fakeRepository{claimFn: func(profiles []string) (ai.DecisionRecord, error) {
		if calls.Add(1) == 1 {
			close(firstClaim)
		}
		if !available.Load() {
			return ai.DecisionRecord{}, ai.ErrNoDecision
		}
		return ai.DecisionRecord{ID: "request", ProfileID: profiles[0]}, nil
	}}
	service := New(repository, "secret", nil)
	request := httptest.NewRequest(http.MethodPost, "/claim", strings.NewReader(`{"workerId":"host-agent","leaseSeconds":5,"waitSeconds":2,"profileIds":["codex-subscription-local"]}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		service.AgentHandler(repository).ServeHTTP(response, request)
		close(done)
	}()
	<-firstClaim
	service.NotifyDecisionAvailable()
	select {
	case <-done:
		t.Fatal("spurious notification ended the long poll")
	case <-time.After(10 * time.Millisecond):
	}
	available.Store(true)
	service.NotifyDecisionAvailable()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("long poll did not wake")
	}
	if response.Code != http.StatusOK || calls.Load() < 2 || !strings.Contains(response.Body.String(), `"requestId":"request"`) {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body.String())
	}
}

func TestAgentClaimLongPollTimesOut(t *testing.T) {
	repository := &fakeRepository{claimErr: ai.ErrNoDecision}
	handler := New(repository, "secret", nil).AgentHandler(repository)
	request := httptest.NewRequest(http.MethodPost, "/claim", strings.NewReader(`{"workerId":"agent","leaseSeconds":5,"waitSeconds":1,"profileIds":["profile"]}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	started := time.Now()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || time.Since(started) < 900*time.Millisecond {
		t.Fatalf("status=%d elapsed=%s", response.Code, time.Since(started))
	}
}

func TestUsageRequiresAcceptedPlan(t *testing.T) {
	for _, status := range []string{"running", "pending", "failed", "fallback", "accepted"} {
		t.Run(status, func(t *testing.T) {
			repository := &fakeRepository{record: ai.DecisionRecord{ID: "request", Status: status}}
			handler := NewWithLogger(repository, "secret", nil, slog.Default()).AgentHandler(repository)
			request := httptest.NewRequest(http.MethodPost, "/usage", strings.NewReader(`{"requestId":"request","usage":{"requests":1}}`))
			request.Header.Set("Authorization", "Bearer secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if status == "accepted" {
				if response.Code != http.StatusNoContent || repository.usage != 1 {
					t.Fatal("accepted plan usage rejected")
				}
			} else if response.Code != http.StatusConflict || repository.usage != 0 {
				t.Fatal("completion without accepted plan acknowledged")
			}
		})
	}
}
