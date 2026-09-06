package runner

import (
	"context"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"strings"
	"testing"
)

type doctrineProvider struct {
	t *testing.T
	turnProvider
}

func (p *doctrineProvider) NextTurn(ctx context.Context, request ai.ModelTurnRequest) (ai.ModelTurnResult, error) {
	if !strings.Contains(request.SystemPrompt, "BEGIN_FACTION_DOCTRINE\n\"Protect income\"") || !strings.HasPrefix(request.SystemPrompt, ai.SystemPrompt) {
		p.t.Fatal("API doctrine missing")
	}
	return p.turnProvider.NextTurn(ctx, request)
}
func TestDoctrineReachesAPIRunner(t *testing.T) {
	result, err := (APIRunner{Provider: &doctrineProvider{t: t}, Connector: fakeConnector{&fakeToolSession{}}}).Run(context.Background(), ai.DecisionSession{RequestID: "request", Doctrine: "Protect income", Profile: ai.ModelProfile{MaxSteps: 3}})
	if err != nil || !result.Accepted {
		t.Fatalf("%+v %v", result, err)
	}
}

type doctrineExecutor struct{ t *testing.T }

func (e doctrineExecutor) Run(_ context.Context, _ string, args []string, _ []string, _ string) ([]byte, error) {
	if !strings.Contains(strings.Join(args, " "), "BEGIN_FACTION_DOCTRINE\n\"Protect income\"") {
		e.t.Fatal("CLI doctrine missing")
	}
	return nil, nil
}
func TestDoctrineReachesCLIRunner(t *testing.T) {
	_, err := (CLIRunner{Provider: "codex-cli", Executor: doctrineExecutor{t}}).Run(context.Background(), ai.DecisionSession{RequestID: "request", Doctrine: "Protect income"})
	if err != nil {
		t.Fatal(err)
	}
}
