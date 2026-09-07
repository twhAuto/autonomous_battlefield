// Checks background-generation tools, capability scoping, agent control, and prompt
// claiming when battle work is absent.

package mcpserver

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/testutil"

	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/runner"
	"github.com/twh09/autonomous_battlefield/backend/internal/generation"
)

type generationRepo struct {
	job      generation.Job
	value    string
	fail     error
	claimed  int
	finished bool
}

func (r *generationRepo) Claim(_ context.Context, _ string, p []string) (generation.Job, error) {
	r.claimed++
	if len(p) != 1 || p[0] != "writer" {
		return generation.Job{}, generation.ErrNotFound
	}
	return r.job, r.fail
}
func (r *generationRepo) Context(_ context.Context, id string) (generation.Job, error) {
	if id != r.job.ID {
		return generation.Job{}, generation.ErrNotFound
	}
	return r.job, r.fail
}
func (r *generationRepo) Submit(_ context.Context, id, value string) (bool, error) {
	if id != r.job.ID {
		return false, generation.ErrNotFound
	}
	if value == "" {
		return false, generation.ErrInvalid
	}
	r.value = value
	return true, r.fail
}
func (r *generationRepo) Finish(_ context.Context, id, _ string, _ ai.Usage, _ map[string]any) (bool, error) {
	r.finished = true
	return id == r.job.ID, r.fail
}

type generationProvider struct {
	t    *testing.T
	turn int
}

func (p *generationProvider) NextTurn(_ context.Context, r ai.ModelTurnRequest) (ai.ModelTurnResult, error) {
	p.turn++
	if strings.Contains(r.SystemPrompt, "submit_strategy_plan") || !strings.Contains(r.SystemPrompt, "submit_faction_background") || len(r.Tools) != 2 {
		p.t.Fatal("generation exposed battle tools")
	}
	for _, tool := range r.Tools {
		if tool.Name != "get_background_context" && tool.Name != "submit_faction_background" {
			p.t.Fatal(tool.Name)
		}
	}
	call := ai.ToolCall{ID: "context", Name: "get_background_context", Arguments: json.RawMessage(`{"requestId":"draft"}`)}
	if p.turn == 2 {
		all, _ := json.Marshal(r.Messages)
		if !strings.Contains(string(all), "Old roots") {
			p.t.Fatal("draft not delivered")
		}
		call = ai.ToolCall{ID: "submit", Name: "submit_faction_background", Arguments: json.RawMessage(`{"requestId":"draft","background":"A new history"}`)}
	}
	return ai.ModelTurnResult{ToolCalls: []ai.ToolCall{call}}, nil
}

type generationCLI struct {
	t        *testing.T
	endpoint string
}

func (e generationCLI) Run(ctx context.Context, _ string, args, env []string, _ string) ([]byte, error) {
	prompt := args[len(args)-1]
	if !strings.Contains(prompt, "get_background_context") || strings.Contains(prompt, "submit_strategy_plan") {
		e.t.Fatal("wrong CLI instruction/tools")
	}
	token := ""
	for _, v := range env {
		if strings.HasPrefix(v, "BATTLEFIELD_MCP_AUTHORIZATION=Bearer ") {
			token = strings.TrimPrefix(v, "BATTLEFIELD_MCP_AUTHORIZATION=Bearer ")
		}
	}
	client, err := (runner.MCPClient{}).Connect(ctx, e.endpoint, token)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	payload, _, err := client.Call(ctx, ai.ToolCall{Name: "get_background_context", Arguments: json.RawMessage(`{"requestId":"draft"}`)})
	if err != nil {
		return nil, err
	}
	if !strings.Contains(string(payload), "Old roots") {
		e.t.Fatal(string(payload))
	}
	_, ok, err := client.Call(ctx, ai.ToolCall{Name: "submit_faction_background", Arguments: json.RawMessage(`{"requestId":"draft","background":"A new history"}`)})
	if err == nil && !ok {
		return nil, errors.New("submission not accepted")
	}
	return nil, err
}
func TestGenerationThroughAPIAndCLITools(t *testing.T) {
	for _, runtime := range []string{"api", "cli"} {
		t.Run(runtime, func(t *testing.T) {
			repo := &generationRepo{job: generation.Job{ID: "draft", Input: generation.Input{Name: "Northwind", Doctrine: "Defend", Background: "Old roots"}}}
			service := New(&fakeRepository{}, "secret", nil).WithGenerations(repo)
			server := httptest.NewServer(service.GenerationHandler())
			defer server.Close()
			token, err := ai.MintCapability("secret", "generation:draft", time.Now().Add(time.Minute))
			testutil.Require(t, err == nil, func() { t.Fatal(err) })
			session := ai.DecisionSession{JobType: generation.JobType, RequestID: "draft", MCPURL: server.URL, CapabilityToken: token, Profile: ai.ModelProfile{MaxSteps: 3, MaxOutputTokens: 1000}}
			var executor ai.DecisionRunner = runner.APIRunner{Provider: &generationProvider{t: t}}
			if runtime == "cli" {
				executor = runner.CLIRunner{Provider: "codex-cli", Executor: generationCLI{t: t, endpoint: server.URL}}
			}
			result, err := executor.Run(context.Background(), session)
			testutil.Require(t, !(err != nil || !result.Accepted || repo.value != "A new history"), func() { t.Fatal(result, err, repo.value) })
			client, err := (runner.MCPClient{}).Connect(context.Background(), server.URL, token)
			testutil.Require(t, err == nil, func() { t.Fatal(err) })
			defer client.Close()
			for _, call := range []ai.ToolCall{{Name: "get_background_context", Arguments: json.RawMessage(`{"requestId":"other"}`)}, {Name: "submit_faction_background", Arguments: json.RawMessage(`{"requestId":"other","background":"attack"}`)}, {Name: "submit_faction_background", Arguments: json.RawMessage(`{"requestId":"draft","background":""}`)}} {
				_, accepted, err := client.Call(context.Background(), call)
				testutil.Require(t, !(err != nil || accepted), func() { t.Fatal(accepted, err) })
			}
		})
	}
}
func TestGenerationCapabilityAndControlPlane(t *testing.T) {
	repo := &generationRepo{job: generation.Job{ID: "draft", ProfileID: "writer", ExecutionSeconds: 120}}
	service := New(&fakeRepository{}, "secret", nil).WithGenerations(repo)
	for _, id := range []string{"", "battle-request"} {
		token, _ := ai.MintCapability("secret", id, time.Now().Add(time.Minute))
		req := httptest.NewRequest("POST", "/generation-mcp", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		service.GenerationHandler().ServeHTTP(w, req)
		testutil.Require(t, w.Code == 401, func() { t.Fatal(w.Code) })
	}
	token, _ := ai.MintCapability("secret", "generation:draft", time.Now().Add(time.Minute))
	req := httptest.NewRequest("POST", "/generation-mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	service.GenerationHandler().ServeHTTP(w, req)
	testutil.Require(t, w.Code == 403, func() { t.Fatal(w.Code) })
	// Generation-only claims never consume battle decisions; omitted types remain battle-only.
	battle := &fakeRepository{record: ai.DecisionRecord{ID: "battle", ProfileID: "writer"}}
	for _, body := range []string{`{"workerId":"w","profileIds":["writer"],"jobTypes":["faction_background"]}`, `{"workerId":"w","profileIds":["writer"]}`, `{"workerId":"w","profileIds":["writer"],"jobTypes":["battle_decision","faction_background"]}`} {
		req = httptest.NewRequest("POST", "/claim", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		w = httptest.NewRecorder()
		service.AgentHandler(battle).ServeHTTP(w, req)
		testutil.Require(t, w.Code == 200, func() { t.Fatal(w.Code, w.Body.String()) })
	}
	testutil.Require(t, repo.claimed == 1, func() { t.Fatal("legacy/mixed worker ignored battle priority", repo.claimed) })
	for _, tc := range []struct {
		body, auth string
		status     int
	}{{`{"requestId":"draft"}`, "Bearer secret", 204}, {`{"requestId":"other"}`, "Bearer secret", 409}, {`{`, "Bearer secret", 400}, {`{}`, "", 401}} {
		req = httptest.NewRequest("POST", "/generation-finish", strings.NewReader(tc.body))
		req.Header.Set("Authorization", tc.auth)
		w = httptest.NewRecorder()
		service.AgentHandler(battle).ServeHTTP(w, req)
		testutil.Require(t, w.Code == tc.status, func() { t.Fatal(w.Code, w.Body.String()) })
	}
}

func TestReadyGenerationDoesNotWaitBehindEmptyBattlePoll(t *testing.T) {
	drafts := &generationRepo{job: generation.Job{ID: "draft", ProfileID: "writer"}}
	battles := &fakeRepository{claimErr: ai.ErrNoDecision}
	service := New(battles, "secret", nil).WithGenerations(drafts)
	req := httptest.NewRequest("POST", "/claim", strings.NewReader(`{"workerId":"w","profileIds":["writer"],"jobTypes":["battle_decision","faction_background"],"waitSeconds":20}`))
	req.Header.Set("Authorization", "Bearer secret")
	ctx, cancel := context.WithTimeout(req.Context(), time.Second)
	defer cancel()
	w := httptest.NewRecorder()
	service.AgentHandler(battles).ServeHTTP(w, req.WithContext(ctx))
	testutil.Require(t, !(w.Code != 200 || ctx.Err() != nil || !strings.Contains(w.Body.String(), `"jobType":"faction_background"`)), func() { t.Fatal(w.Code, w.Body.String(), ctx.Err()) })
}
