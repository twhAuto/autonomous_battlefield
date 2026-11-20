package runner

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
)

func TestIdentityDedupAcrossAPIToolAndCLIPrefetch(t *testing.T) {
	payload := json.RawMessage(`{"context":{"requestId":"request","targetTick":73,"expectedVersion":1,"background":"Full story","doctrine":"Defend"}}`)
	identity := ai.DecisionSession{RequestID: "request", Background: "Full story", Doctrine: "Defend"}
	session := contextFixtureSession{payload: payload}
	api := identityContextSession{DecisionToolSession: session, identity: identity}
	got, accepted, err := api.Call(context.Background(), ai.ToolCall{Name: "get_decision_context"})
	if err != nil || accepted {
		t.Fatal(err)
	}
	want := `{"context":{"requestId":"request","targetTick":73,"expectedVersion":1}}`
	if string(got) != want {
		t.Fatal(string(got))
	}
	cli := CLIRunner{Connector: errorConnector{session: session}}
	got, err = cli.prefetchContext(context.Background(), identity)
	if err != nil || string(got) != want {
		t.Fatal(string(got), err)
	}
	if _, _, err = api.Call(context.Background(), ai.ToolCall{Name: "submit_strategy_plan"}); err == nil {
		t.Fatal("underlying error swallowed")
	}
}
