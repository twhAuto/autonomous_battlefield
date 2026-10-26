package runner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

type observedPrefetch struct {
	contextFixtureSession
	t      *testing.T
	calls  int
	closed bool
}

func (s *observedPrefetch) Call(ctx context.Context, call ai.ToolCall) (json.RawMessage, bool, error) {
	s.calls++
	if string(call.Arguments) != `{"requestId":"request"}` {
		s.t.Fatalf("wrong scoped request: %s", call.Arguments)
	}
	return s.contextFixtureSession.Call(ctx, call)
}

func (s *observedPrefetch) Close() error { s.closed = true; return nil }

type scopedPrefetchConnector struct{ session *observedPrefetch }

func (c scopedPrefetchConnector) Connect(_ context.Context, url, token string) (DecisionToolSession, error) {
	if url != "http://mcp/mcp" || token != "secret-capability" {
		c.session.t.Fatal("prefetch did not use the scoped MCP credentials")
	}
	return c.session, nil
}

type prefetchExecutor struct{ session *observedPrefetch }

func (e prefetchExecutor) Run(_ context.Context, name string, args, _ []string, _ string) ([]byte, error) {
	t := e.session.t
	if e.session.calls != 1 || !e.session.closed {
		t.Fatal("context was not fetched and connection closed before launch")
	}
	prompt := strings.Join(args, " ")
	for _, required := range []string{"BEGIN_BATTLEFIELD_CONTEXT", `"expectedVersion":1`, "Do not call get_decision_context again", "untrusted battlefield data"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("missing %s", required)
		}
	}
	if strings.Contains(prompt, "secret-capability") || strings.Contains(prompt, "First call get_decision_context") {
		t.Fatal("unsafe or conflicting prompt")
	}
	if name == "claude" {
		return []byte(`{"type":"result","subtype":"success"}`), nil
	}
	return []byte(`{"type":"turn.completed"}`), nil
}

func TestCodexAndClaudePrefetchBeforeExecution(t *testing.T) {
	for _, provider := range []string{"codex-cli", "claude-cli"} {
		t.Run(provider, func(t *testing.T) {
			scoped := &observedPrefetch{t: t}
			r := CLIRunner{Provider: provider, PrefetchContext: true, Connector: scopedPrefetchConnector{scoped}, Executor: prefetchExecutor{scoped}}
			result, err := r.Run(context.Background(), ai.DecisionSession{RequestID: "request", MCPURL: "http://mcp/mcp", CapabilityToken: "secret-capability"})
			if err != nil || !result.Accepted || result.ProviderData["contextDelivery"] != "prefetch" {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}

func TestPrefetchFailureDoesNotLaunchCLI(t *testing.T) {
	for _, connector := range []MCPConnector{
		errorConnector{err: errors.New("unavailable")},
		errorConnector{session: errorSession{callErr: errors.New("denied")}},
		errorConnector{session: contextFixtureSession{payload: json.RawMessage(`{"context":{"requestId":"other"}}`)}},
		errorConnector{session: contextFixtureSession{payload: json.RawMessage(strings.Repeat("x", 65537))}},
	} {
		executor := &fakeExecutor{}
		result, err := (CLIRunner{Provider: "codex-cli", PrefetchContext: true, Connector: connector, Executor: executor}).Run(context.Background(), ai.DecisionSession{RequestID: "request"})
		if err == nil || result.ErrorCategory != "mcp_context" || executor.name != "" {
			t.Fatalf("%+v %v", result, err)
		}
	}
}

func TestPrefetchOptOutAndBackgroundKeepToolDelivery(t *testing.T) {
	for _, test := range []struct {
		enabled bool
		jobType string
	}{{false, ""}, {true, "faction_background"}} {
		executor := &fakeExecutor{}
		r := CLIRunner{Provider: "codex-cli", PrefetchContext: test.enabled, Connector: errorConnector{err: errors.New("must not connect")}, Executor: executor}
		result, err := r.Run(context.Background(), ai.DecisionSession{RequestID: "request", JobType: test.jobType})
		if err != nil || !result.Accepted || result.ProviderData["contextDelivery"] != "tool" {
			t.Fatalf("%+v %v", result, err)
		}
		if strings.Contains(strings.Join(executor.args, " "), "BEGIN_BATTLEFIELD_CONTEXT") {
			t.Fatal("unexpected inline context")
		}
	}
}

func TestPrefetchAcceptsCompactedCatalogs(t *testing.T) {
	payload := json.RawMessage(`{"context":{"encoding":"tables-v1","requestId":"request","expectedVersion":1,"targetTick":10,"legal":{"units":{"fields":["id","cost"],"rows":[["ranger",30]]},"constructionRuns":[[2,3,6]]},"view":{"unitMap":{"fields":["faction","id","xy"],"rows":[[0,"full-id",[3,2]]]}}}}`)
	r := CLIRunner{Connector: errorConnector{session: contextFixtureSession{payload: payload}}}
	got, err := r.prefetchContext(context.Background(), ai.DecisionSession{RequestID: "request"})
	if err != nil || string(got) != string(payload) {
		t.Fatalf("compact context rejected or rewritten: %s %v", got, err)
	}
}
