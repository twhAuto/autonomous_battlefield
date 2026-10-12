// Exercises agent dispatch authorization, claims and long polling, usage reporting, and
// separation of the worker and capability secrets.

package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/capability"
	"github.com/twh09/autonomous_battlefield/backend/internal/generation"
)

func TestAgentControlPlaneAuthorizationAndEmptyQueue(t *testing.T) {
	repository := &fakeRepository{claimErr: ai.ErrNoDecision}
	handler := New(repository, "secret", nil).WithAgentSecret("worker-secret").AgentHandler(repository)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/claim", strings.NewReader(`{}`)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatal(unauthorized.Code)
	}
	request := httptest.NewRequest(http.MethodPost, "/claim", strings.NewReader(`{"workerId":"agent","leaseSeconds":1,"profileIds":["profile"]}`))
	request.Header.Set("Authorization", "Bearer worker-secret")
	empty := httptest.NewRecorder()
	handler.ServeHTTP(empty, request)
	if empty.Code != http.StatusNoContent {
		t.Fatal(empty.Code)
	}
}

func TestAgentControlPlaneRejectsMalformedBodies(t *testing.T) {
	handler := New(&fakeRepository{}, "secret", nil).WithAgentSecret("worker-secret").AgentHandler(&fakeRepository{})
	for _, path := range []string{"/claim", "/fail", "/usage"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("bad"))
		request.Header.Set("Authorization", "Bearer worker-secret")
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
	handler := NewWithLogger(repository, "secret", nil, slog.New(slog.NewJSONHandler(&logs, nil))).WithAgentSecret("worker-secret").AgentHandler(repository)
	claim := httptest.NewRequest(http.MethodPost, "/claim", strings.NewReader(`{"workerId":"agent","leaseSeconds":5,"profileIds":["profile","profile","  "]}`))
	claim.Header.Set("Authorization", "Bearer worker-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, claim)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code)
	}
	for path, body := range map[string]string{"/fail": `{"requestId":"request","category":"provider","metadata":{"runnerOutcome":"deadline_exceeded"}}`, "/usage": `{"requestId":"request","usage":{"requests":1}}`} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer worker-secret")
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
	request.Header.Set("Authorization", "Bearer worker-secret")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		service.WithAgentSecret("worker-secret").AgentHandler(repository).ServeHTTP(response, request)
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
	handler := New(repository, "secret", nil).WithAgentSecret("worker-secret").AgentHandler(repository)
	request := httptest.NewRequest(http.MethodPost, "/claim", strings.NewReader(`{"workerId":"agent","leaseSeconds":5,"waitSeconds":1,"profileIds":["profile"]}`))
	request.Header.Set("Authorization", "Bearer worker-secret")
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
			handler := NewWithLogger(repository, "secret", nil, slog.Default()).WithAgentSecret("worker-secret").AgentHandler(repository)
			request := httptest.NewRequest(http.MethodPost, "/usage", strings.NewReader(`{"requestId":"request","usage":{"requests":1}}`))
			request.Header.Set("Authorization", "Bearer worker-secret")
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

func TestWorkerAuthenticationNeverFallsBackToCapabilitySecret(t *testing.T) {
	s := New(&fakeRepository{}, "signing-secret", nil)
	r := httptest.NewRequest("POST", "/claim", nil)
	for _, workerSecret := range []string{"", "signing-secret", "worker-secret"} {
		s.WithAgentSecret(workerSecret)
		for _, provided := range []string{"", "signing-secret", "worker-secret"} {
			r.Header.Set("Authorization", "Bearer "+provided)
			want := workerSecret == "worker-secret" && provided == workerSecret
			if s.agentAuthorized(r) != want {
				t.Fatalf("secret separation failed for configured %q provided %q", workerSecret, provided)
			}
		}
	}
	token := s.mint("request-one", time.Minute)
	id, e := capability.Verify("signing-secret", token, time.Now())
	if e != nil || id != "request-one" {
		t.Fatal(id, e)
	}
	if _, e = capability.Verify("worker-secret", token, time.Now()); e == nil {
		t.Fatal("worker key verified capability")
	}
}

type policyRepository struct {
	*fakeRepository
	enabled bool
	err     error
}

func (r policyRepository) AgentEnabled(context.Context) (bool, error) { return r.enabled, r.err }

func agentRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer worker-secret")
	return request
}

func serveAgent(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestAgentEnabledReflectsOperatorPolicy(t *testing.T) {
	for name, tc := range map[string]struct {
		repository ai.DecisionRepository
		status     int
	}{
		"policy unsupported": {&fakeRepository{}, http.StatusServiceUnavailable},
		"policy read fails":  {policyRepository{&fakeRepository{}, true, errors.New("database down")}, http.StatusServiceUnavailable},
		"AI stopped":         {policyRepository{&fakeRepository{}, false, nil}, http.StatusServiceUnavailable},
		"AI enabled":         {policyRepository{&fakeRepository{}, true, nil}, http.StatusNoContent},
	} {
		handler := New(tc.repository, "secret", nil).WithAgentSecret("worker-secret").AgentHandler(tc.repository)
		if response := serveAgent(handler, agentRequest(http.MethodGet, "/enabled", "")); response.Code != tc.status {
			t.Fatalf("%s: status=%d want %d", name, response.Code, tc.status)
		}
	}
	repository := policyRepository{&fakeRepository{}, true, nil}
	handler := New(repository, "secret", nil).WithAgentSecret("worker-secret").AgentHandler(repository)
	if response := serveAgent(handler, httptest.NewRequest(http.MethodGet, "/enabled", nil)); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated policy read=%d", response.Code)
	}
}

func TestAgentClaimRejectsUnknownJobTypesAndBlankProfiles(t *testing.T) {
	repository := &fakeRepository{}
	handler := New(repository, "secret", nil).WithAgentSecret("worker-secret").AgentHandler(repository)
	for _, body := range []string{
		`{"workerId":"agent","profileIds":["profile"],"jobTypes":["shell"]}`,
		`{"workerId":"agent","profileIds":["  ",""]}`,
	} {
		if response := serveAgent(handler, agentRequest(http.MethodPost, "/claim", body)); response.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d", body, response.Code)
		}
	}
	if repository.claimedProfiles != nil {
		t.Fatal("rejected claim reached the repository")
	}
}

func TestAgentDispatchSurfacesRepositoryFailures(t *testing.T) {
	claim := `{"workerId":"agent","profileIds":["profile"]}`
	for name, tc := range map[string]struct {
		repository *fakeRepository
		path, body string
		status     int
	}{
		"claim query fails":       {&fakeRepository{claimErr: errors.New("database down")}, "/claim", claim, http.StatusInternalServerError},
		"claim without deadline":  {&fakeRepository{noDeadline: true}, "/claim", claim, http.StatusServiceUnavailable},
		"failure report fails":    {&fakeRepository{failErr: errors.New("database down")}, "/fail", `{"requestId":"request"}`, http.StatusInternalServerError},
		"usage lookup fails":      {&fakeRepository{err: errors.New("database down")}, "/usage", `{"requestId":"request"}`, http.StatusInternalServerError},
		"usage write fails":       {&fakeRepository{record: ai.DecisionRecord{Status: "accepted"}, usageErr: errors.New("database down")}, "/usage", `{"requestId":"request"}`, http.StatusInternalServerError},
		"usage without plan":      {&fakeRepository{record: ai.DecisionRecord{Status: "fallback"}}, "/usage", `{"requestId":"request"}`, http.StatusConflict},
		"failure report succeeds": {&fakeRepository{}, "/fail", `{"requestId":"request"}`, http.StatusNoContent},
	} {
		handler := New(tc.repository, "secret", nil).WithAgentSecret("worker-secret").AgentHandler(tc.repository)
		response := serveAgent(handler, agentRequest(http.MethodPost, tc.path, tc.body))
		if response.Code != tc.status {
			t.Fatalf("%s: status=%d want %d", name, response.Code, tc.status)
		}
		if tc.status >= 500 && strings.Contains(response.Body.String(), "database down") {
			t.Fatalf("%s: leaked repository error %q", name, response.Body.String())
		}
	}
}

func TestAgentFailureLogTruncatesRunnerOutcome(t *testing.T) {
	var logs bytes.Buffer
	repository := &fakeRepository{}
	handler := NewWithLogger(repository, "secret", nil, slog.New(slog.NewJSONHandler(&logs, nil))).WithAgentSecret("worker-secret").AgentHandler(repository)
	outcome := strings.Repeat("x", 200)
	body := `{"requestId":"request","metadata":{"runnerOutcome":"` + outcome + `"}}`
	if response := serveAgent(handler, agentRequest(http.MethodPost, "/fail", body)); response.Code != http.StatusNoContent {
		t.Fatal(response.Code)
	}
	if !strings.Contains(logs.String(), `"runner_outcome":"`+outcome[:128]+`"`) || strings.Contains(logs.String(), outcome[:129]) {
		t.Fatalf("runner outcome not bounded to 128 bytes: %s", logs.String())
	}
}

func TestMixedClaimFallsBackToBattleLongPoll(t *testing.T) {
	drafts := &generationRepo{job: generation.Job{ID: "draft", ProfileID: "writer"}}
	body := `{"workerId":"w","profileIds":["battle-profile"],"jobTypes":["battle_decision","faction_background"],"waitSeconds":1}`
	var calls atomic.Int32
	found := &fakeRepository{claimFn: func([]string) (ai.DecisionRecord, error) {
		if calls.Add(1) == 1 {
			return ai.DecisionRecord{}, ai.ErrNoDecision
		}
		return ai.DecisionRecord{ID: "battle", ProfileID: "battle-profile"}, nil
	}}
	service := New(found, "secret", nil).WithGenerations(drafts).WithAgentSecret("worker-secret")
	if response := serveAgent(service.AgentHandler(found), agentRequest(http.MethodPost, "/claim", body)); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"requestId":"battle"`) {
		t.Fatalf("battle job after empty draft queue: %d %s", response.Code, response.Body.String())
	}
	calls.Store(0)
	failing := &fakeRepository{claimFn: func([]string) (ai.DecisionRecord, error) {
		if calls.Add(1) == 1 {
			return ai.DecisionRecord{}, ai.ErrNoDecision
		}
		return ai.DecisionRecord{}, errors.New("database down")
	}}
	if response := serveAgent(service.AgentHandler(failing), agentRequest(http.MethodPost, "/claim", body)); response.Code != http.StatusInternalServerError {
		t.Fatalf("failed battle poll after empty draft queue: %d", response.Code)
	}
	empty := &fakeRepository{claimErr: ai.ErrNoDecision}
	short := `{"workerId":"w","profileIds":["battle-profile"],"jobTypes":["battle_decision","faction_background"]}`
	if response := serveAgent(service.AgentHandler(empty), agentRequest(http.MethodPost, "/claim", short)); response.Code != http.StatusNoContent {
		t.Fatalf("empty mixed claim without wait: %d", response.Code)
	}
}

func TestAgentClaimLongPollEndsWhenWorkerDisconnects(t *testing.T) {
	repository := &fakeRepository{claimErr: ai.ErrNoDecision}
	handler := New(repository, "secret", nil).WithAgentSecret("worker-secret").AgentHandler(repository)
	ctx, cancel := context.WithCancel(context.Background())
	request := agentRequest(http.MethodPost, "/claim", `{"workerId":"agent","profileIds":["profile"],"waitSeconds":20}`).WithContext(ctx)
	time.AfterFunc(50*time.Millisecond, cancel)
	started := time.Now()
	if response := serveAgent(handler, request); response.Code != http.StatusNoContent || time.Since(started) > 5*time.Second {
		t.Fatalf("disconnect did not end the long poll: %d after %s", response.Code, time.Since(started))
	}
}

func TestGenerationDispatchFailures(t *testing.T) {
	broken := &generationRepo{job: generation.Job{ID: "draft", ProfileID: "writer"}, fail: errors.New("database down")}
	battles := &fakeRepository{claimErr: ai.ErrNoDecision}
	service := New(battles, "secret", nil).WithGenerations(broken).WithAgentSecret("worker-secret")
	claim := `{"workerId":"w","profileIds":["writer"],"jobTypes":["faction_background"]}`
	if response := serveAgent(service.AgentHandler(battles), agentRequest(http.MethodPost, "/claim", claim)); response.Code != http.StatusInternalServerError {
		t.Fatalf("generation claim failure=%d", response.Code)
	}
	if response := serveAgent(service.AgentHandler(battles), agentRequest(http.MethodPost, "/generation-finish", `{"requestId":"draft"}`)); response.Code != http.StatusInternalServerError {
		t.Fatalf("generation report failure=%d", response.Code)
	}
	withoutGenerations := New(battles, "secret", nil).WithAgentSecret("worker-secret")
	if response := serveAgent(withoutGenerations.AgentHandler(battles), agentRequest(http.MethodPost, "/generation-finish", `{"requestId":"draft"}`)); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("generation report without generation support=%d", response.Code)
	}
	if response := serveAgent(withoutGenerations.AgentHandler(battles), agentRequest(http.MethodPost, "/claim", claim)); response.Code != http.StatusNoContent {
		t.Fatalf("generation-only claim without generation support=%d", response.Code)
	}
}
