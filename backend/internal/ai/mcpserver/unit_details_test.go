package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/runner"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func compactRecord() ai.DecisionRecord {
	rules := game.Ruleset{ID: "rules", Version: "1", Units: []game.UnitType{{ID: "ranger", Cost: 10, Attack: 23}}, Knowledge: &game.RulesKnowledge{Mechanics: "Combat", Cards: []game.UnitCard{{UnitID: "ranger", Role: "ranged", Guidance: "keep-distance-marker"}}}}
	c := ai.DecisionContext{SchemaVersion: ai.ContextSchema, RequestID: "sample", TargetTick: 20, ExpectedVersion: 1, Legal: ai.LegalChoices{Units: rules.Units, ActionKinds: []string{"attack"}}}
	c.UnitReference = ai.FreezeUnitReference(rules, c)
	return ai.DecisionRecord{ID: "sample", Version: 1, TargetTick: 20, Status: "running", Observation: c}
}

type compactProvider struct {
	t      *testing.T
	turn   int
	lookup bool
}

func (p *compactProvider) NextTurn(_ context.Context, request ai.ModelTurnRequest) (ai.ModelTurnResult, error) {
	p.turn++
	name, args := "get_decision_context", `{"requestId":"sample"}`
	if p.turn == 2 {
		messages, _ := json.Marshal(request.Messages)
		if strings.Contains(string(messages), "keep-distance-marker") || strings.Contains(string(messages), `\"stats\"`) {
			p.t.Fatal("reference leaked into initial prompt")
		}
		if p.lookup {
			name = "get_unit_details"
			args = `{"requestId":"sample","unitIds":["ranger"]}`
		}
	}
	if p.turn == 3 && p.lookup {
		messages, _ := json.Marshal(request.Messages)
		if !strings.Contains(string(messages), "keep-distance-marker") {
			p.t.Fatal("lookup was not delivered")
		}
	}
	if (p.turn == 2 && !p.lookup) || p.turn == 3 {
		name = "submit_strategy_plan"
		args = `{"requestId":"sample","idempotencyKey":"one","expectedVersion":1,"targetTick":20,"name":"Defend","rationale":"Visible pressure warrants defense.","actionWeights":{"attack":1000},"reserveGold":0,"spendOrder":[],"constructionPriorities":[],"recruitmentPriorities":[],"recruitmentOrders":[],"upgradeOrders":[]}`
	}
	return ai.ModelTurnResult{ToolCalls: []ai.ToolCall{{Name: name, Arguments: json.RawMessage(args)}}, Usage: ai.Usage{InputTokens: 10}}, nil
}

type compactCLI struct {
	t       *testing.T
	session ai.DecisionSession
	lookup  bool
}

func (e compactCLI) Run(ctx context.Context, _ string, args, env []string, _ string) ([]byte, error) {
	if !strings.Contains(strings.Join(args, " "), "get_unit_details") {
		e.t.Fatal("CLI tool not enabled")
	}
	client, err := (runner.MCPClient{}).Connect(ctx, e.session.MCPURL, e.session.CapabilityToken)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	p := compactProvider{t: e.t, lookup: e.lookup}
	request := ai.ModelTurnRequest{}
	for range 3 {
		turn, _ := p.NextTurn(ctx, request)
		call := turn.ToolCalls[0]
		payload, accepted, err := client.Call(ctx, call)
		if err != nil {
			return nil, err
		}
		request.Messages = append(request.Messages, ai.Message{Role: "tool", Content: string(payload)})
		if accepted {
			return []byte(`{}`), nil
		}
	}
	return nil, errors.New("not submitted")
}

func TestCompactDecisionThroughAPIAndCLI(t *testing.T) {
	for _, runtime := range []string{"api", "cli"} {
		for _, lookup := range []bool{false, true} {
			t.Run(runtime+map[bool]string{true: "-lookup", false: "-direct"}[lookup], func(t *testing.T) {
				repo := &fakeRepository{record: compactRecord()}
				server := httptest.NewServer(New(repo, "secret", nil).Handler())
				defer server.Close()
				token, _ := ai.MintCapability("secret", "sample", time.Now().Add(time.Minute))
				session := ai.DecisionSession{RequestID: "sample", MCPURL: server.URL, CapabilityToken: token, Profile: ai.ModelProfile{MaxSteps: 3, MaxOutputTokens: 1000}}
				var executor ai.DecisionRunner = runner.APIRunner{Provider: &compactProvider{t: t, lookup: lookup}}
				if runtime == "cli" {
					executor = runner.CLIRunner{Provider: "codex-cli", Executor: compactCLI{t: t, session: session, lookup: lookup}}
				}
				result, err := executor.Run(context.Background(), session)
				if err != nil || !result.Accepted || repo.submitted.RequestID != "sample" {
					t.Fatal(result, err)
				}
			})
		}
	}
}
func TestUnitDetailsToolAuthorizationAndErrors(t *testing.T) {
	repo := &fakeRepository{record: compactRecord()}
	server := httptest.NewServer(New(repo, "secret", nil).Handler())
	defer server.Close()
	token, _ := ai.MintCapability("secret", "sample", time.Now().Add(time.Minute))
	client, err := (runner.MCPClient{}).Connect(context.Background(), server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, args := range []string{`{"requestId":"other","unitIds":["ranger"]}`, `{"requestId":"sample","unitIds":[]}`, `{"requestId":"sample","unitIds":["ranger","ranger"]}`, `{"requestId":"sample"}`} {
		if payload, _, err := client.Call(context.Background(), ai.ToolCall{Name: "get_unit_details", Arguments: []byte(args)}); err == nil && !strings.Contains(string(payload), `"error"`) {
			t.Fatalf("allowed %s", args)
		}
	}
	payload, accepted, err := client.Call(context.Background(), ai.ToolCall{Name: "get_unit_details", Arguments: []byte(`{"requestId":"sample","unitIds":["hidden","unknown"]}`)})
	if err != nil || accepted || strings.Contains(string(payload), `"available":true`) {
		t.Fatal(string(payload), err)
	}
	repo.err = errors.New("storage failed")
	if payload, _, err := client.Call(context.Background(), ai.ToolCall{Name: "get_unit_details", Arguments: []byte(`{"requestId":"sample","unitIds":["ranger"]}`)}); err == nil && !strings.Contains(string(payload), `"error"`) {
		t.Fatal("storage error ignored")
	}
}
