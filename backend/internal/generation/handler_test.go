package generation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
)

type httpRepo struct {
	actor, key string
	input      Input
	err        error
	calls      int
}

func (r *httpRepo) Options(_ context.Context, a string, _ Config) (Options, error) {
	r.actor = a
	return Options{Remaining: 9, ResetAt: time.Now().Add(time.Hour), RetryAt: time.Now().Add(time.Second)}, r.err
}
func (r *httpRepo) Admit(_ context.Context, a, k string, i Input, _ Config, _ string) (Job, Options, error) {
	r.calls++
	r.actor = a
	r.key = k
	r.input = i
	return Job{ID: "job", Status: "queued"}, Options{Remaining: 0, RetryAt: time.Now().Add(time.Hour)}, r.err
}
func (r *httpRepo) Get(_ context.Context, a, id string) (Job, error) {
	r.actor = a
	return Job{ID: id, Status: "succeeded", Background: "A history"}, r.err
}
func generationHTTP(t *testing.T, r Repository, enabled bool) http.Handler {
	t.Helper()
	c := DefaultConfig()
	c.Enabled = enabled
	c.ProfileID = "writer"
	profiles := ai.NewProfileRegistry("cluster", []ai.ModelProfile{{ID: "writer", DisplayName: "Writer", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true, MaxSteps: 3, MaxOutputTokens: 1000}})
	mux := http.NewServeMux()
	(&Handler{Repository: r, Config: c, Profiles: profiles}).RegisterRoutes(mux)
	return authn.RequireAuthentication(false, authn.NewManager(authn.Config{}), mux)
}
func TestGenerationEndpoints(t *testing.T) {
	r := &httpRepo{}
	h := generationHTTP(t, r, true)
	cases := []struct {
		method, path, body string
		err                error
		status             int
	}{
		{"GET", "/options", "", nil, 200},
		{"POST", "", `{"name":" A ","doctrine":" d ","background":" old ","idempotencyKey":"key"}`, nil, 202},
		{"POST", "", `{"name":"A","doctrine":"d"}`, ErrLimited, 429},
		{"POST", "", `{"name":"A","doctrine":"d"}`, ErrConflict, 409},
		{"POST", "", `{"name":"A","doctrine":"d"}`, ErrInvalid, 400},
		{"POST", "", `{"name":"A","doctrine":"d"}`, errors.New("database"), 500},
		{"POST", "", `{`, nil, 400}, {"POST", "", `{"name":" "}`, nil, 400},
		{"GET", "/job", "", nil, 200}, {"GET", "/other", "", ErrNotFound, 404}, {"GET", "/job", "", errors.New("database"), 500}, {"GET", "/options", "", errors.New("database"), 500},
	}
	for _, tt := range cases {
		r.err = tt.err
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tt.method, "/v1/faction-background-generations"+tt.path, strings.NewReader(tt.body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(w, req)
		if w.Code != tt.status {
			t.Fatalf("%s %s: %d %s", tt.method, tt.path, w.Code, w.Body.String())
		}
		if w.Code == 429 {
			var o Options
			if json.Unmarshal(w.Body.Bytes(), &o) != nil || !o.Available || w.Header().Get("Retry-After") == "" {
				t.Fatal(w.Body.String())
			}
		}
	}
	if r.actor != "local-development" {
		t.Fatal(r.actor)
	}
	r.err = nil
	h = generationHTTP(t, r, false)
	before := r.calls
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/faction-background-generations", strings.NewReader(`{"name":"A","doctrine":"d"}`)))
	if w.Code != 503 || r.calls != before {
		t.Fatal("disabled reserved quota", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/faction-background-generations/options", nil))
	var o Options
	_ = json.Unmarshal(w.Body.Bytes(), &o)
	if w.Code != 200 || o.Available || o.Reason == "" {
		t.Fatal(o)
	}
	mux := http.NewServeMux()
	(&Handler{Repository: r}).RegisterRoutes(mux)
	for _, method := range []string{"GET", "POST"} {
		w = httptest.NewRecorder()
		path := "/v1/faction-background-generations"
		if method == "GET" {
			path += "/job"
		}
		mux.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
}
