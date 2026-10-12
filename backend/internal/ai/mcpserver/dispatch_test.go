// Exercises agent dispatch authorization, claims and long polling, usage reporting, and
// separation of the worker and capability secrets.

package mcpserver

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
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
	id, e := ai.VerifyCapability("signing-secret", token, time.Now())
	if e != nil || id != "request-one" {
		t.Fatal(id, e)
	}
	if _, e = ai.VerifyCapability("worker-secret", token, time.Now()); e == nil {
		t.Fatal("worker key verified capability")
	}
}
