// Checks Claude request shapes, tool-call and usage decoding, and the unchanged replay
// of a turn's content.

package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

const anthropicToolTurn = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"tool_use",` +
	`"content":[{"type":"thinking","thinking":"","signature":"sig-1"},{"type":"text","text":"checking"},{"type":"tool_use","id":"toolu_1","name":"get_decision_context","input":{"requestId":"r"}}],` +
	`"usage":{"input_tokens":10,"cache_read_input_tokens":20,"cache_creation_input_tokens":5,"output_tokens":40,"output_tokens_details":{"thinking_tokens":30}}}`

// anthropicCapture answers every request with body and records the last request body.
func anthropicCapture(t *testing.T, body string, request *map[string]any) *http.Client {
	return clientWith(http.StatusOK, body, func(r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("Authorization") != "Bearer key" || r.Header.Get("Anthropic-Version") == "" {
			t.Errorf("unexpected request: %s %v", r.URL.Path, r.Header)
		}
		payload, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(payload, request); err != nil {
			t.Errorf("request body: %v", err)
		}
	})
}

// sameJSON compares two JSON values regardless of key order.
func sameJSON(t *testing.T, label string, got any, want string) {
	t.Helper()
	var expected any
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(expected)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("%s=%s\nwant %s", label, gotJSON, wantJSON)
	}
}

func TestAnthropicSendsConversationToolsAndEffort(t *testing.T) {
	var request map[string]any
	provider := &Anthropic{APIKey: "key", Model: "claude-opus-5-5", ReasoningEffort: "high", Client: anthropicCapture(t, `{"content":[]}`, &request)}
	if _, err := provider.NextTurn(context.Background(), conversationRequest()); err != nil {
		t.Fatal(err)
	}
	if request["model"] != "claude-opus-5-5" || request["max_tokens"] != float64(100) || request["tool_choice"] != nil || request["thinking"] != nil {
		t.Fatalf("request=%v", request)
	}
	sameJSON(t, "system", request["system"], `[{"type":"text","text":"system"}]`)
	sameJSON(t, "output_config", request["output_config"], `{"effort":"high"}`)
	sameJSON(t, "tools", request["tools"], `[{"name":"get_decision_context","description":"context","input_schema":{"type":"object","additionalProperties":false,"required":["requestId"],"properties":{"requestId":{"type":"string"},"guidanceOffset":{"type":"integer"}},"x-extension":"preserved"}}]`)
	sameJSON(t, "messages", request["messages"], `[`+
		`{"role":"user","content":[{"type":"text","text":"decide"}]},`+
		`{"role":"assistant","content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"call-a","name":"get_decision_context","input":{"requestId":"r"}},{"type":"tool_use","id":"call-b","name":"get_unit_details","input":{"requestId":"r","unitIds":["ranger"]}}]},`+
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-a","is_error":false,"content":[{"type":"text","text":"{\"context\":{\"requestId\":\"r\"}}"}]},{"type":"tool_result","tool_use_id":"call-b","is_error":false,"content":[{"type":"text","text":"not an object"}]}]}]`)
}

// The shared prefix and the end of each request are cache breakpoints; the text
// Claude receives is the same message split in two.
func TestAnthropicMarksSharedPrefixAndRequestForCaching(t *testing.T) {
	var request map[string]any
	turn := providerRequest()
	turn.Messages[0] = ai.Message{Role: "user", Content: "shared policy\nfaction and request", SharedPrefixBytes: len("shared policy\n")}
	if _, err := (&Anthropic{APIKey: "key", Client: anthropicCapture(t, `{"content":[]}`, &request)}).NextTurn(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "cache_control", request["cache_control"], `{"type":"ephemeral"}`)
	sameJSON(t, "messages", request["messages"], `[{"role":"user","content":[`+
		`{"type":"text","text":"shared policy\n","cache_control":{"type":"ephemeral"}},`+
		`{"type":"text","text":"faction and request"}]}]`)
	body, _ := json.Marshal(request)
	if breakpoints := strings.Count(string(body), `"cache_control"`); breakpoints != 2 {
		t.Fatalf("breakpoints = %d, want 2 (Claude allows at most 4)", breakpoints)
	}
	for _, shared := range []int{0, len(turn.Messages[0].Content)} {
		turn.Messages[0].SharedPrefixBytes = shared
		if _, err := (&Anthropic{APIKey: "key", Client: anthropicCapture(t, `{"content":[]}`, &request)}).NextTurn(context.Background(), turn); err != nil {
			t.Fatal(err)
		}
		sameJSON(t, "messages", request["messages"], `[{"role":"user","content":[{"type":"text","text":"shared policy\nfaction and request"}]}]`)
	}
}

func TestAnthropicOmitsEffortWhenUnset(t *testing.T) {
	var request map[string]any
	if _, err := (&Anthropic{APIKey: "key", Client: anthropicCapture(t, `{"content":[]}`, &request)}).NextTurn(context.Background(), providerRequest()); err != nil {
		t.Fatal(err)
	}
	if _, ok := request["output_config"]; ok {
		t.Fatalf("output_config sent without an effort: %v", request["output_config"])
	}
}

func TestAnthropicDecodesTurnAndReplaysItUnchanged(t *testing.T) {
	var request map[string]any
	client := anthropicCapture(t, anthropicToolTurn, &request)
	provider := &Anthropic{APIKey: "key", Client: client}
	result, err := provider.NextTurn(context.Background(), providerRequest())
	if err != nil || result.Text != "checking" || len(result.ToolCalls) != 1 || result.Model != "claude-opus-5-5" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if call := result.ToolCalls[0]; call.ID != "toolu_1" || call.Name != "get_decision_context" || string(call.Arguments) != `{"requestId":"r"}` {
		t.Fatalf("call=%+v", call)
	}
	usage := result.Usage
	if !usage.TokensReported || usage.InputTokens != 35 || usage.OutputTokens != 40 || *usage.CachedInputTokens != 20 || *usage.ReasoningOutputTokens != 30 || usage.Requests != 1 {
		t.Fatalf("usage=%+v", usage)
	}
	next := providerRequest()
	next.Messages = append(next.Messages,
		ai.Message{Role: "assistant", Content: result.Text, ToolCalls: result.ToolCalls, ProviderContent: result.ProviderContent},
		ai.Message{Role: "tool", ToolCallID: "toolu_1", Name: "get_decision_context", Content: `{}`},
	)
	if _, err := provider.NextTurn(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	messages, _ := request["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages=%v", request["messages"])
	}
	sameJSON(t, "assistant", messages[1], `{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"sig-1"},{"type":"text","text":"checking"},{"type":"tool_use","id":"toolu_1","name":"get_decision_context","input":{"requestId":"r"}}]}`)
}

func TestAnthropicRejectsUnreadableReplayContent(t *testing.T) {
	request := providerRequest()
	request.Messages = append(request.Messages, ai.Message{Role: "assistant", ProviderContent: json.RawMessage(`{"not":"blocks"}`)})
	if _, err := (&Anthropic{Client: clientWith(http.StatusOK, `{}`, nil)}).NextTurn(context.Background(), request); err == nil {
		t.Fatal("unreadable provider content was sent")
	}
}

func TestAnthropicRecognizesRefusalAndMissingUsage(t *testing.T) {
	refusal := clientWith(http.StatusOK, `{"stop_reason":"refusal","content":[],"usage":{"input_tokens":3,"output_tokens":0}}`, nil)
	result, err := (&Anthropic{Client: refusal}).NextTurn(context.Background(), providerRequest())
	if !errors.Is(err, ErrRefusal) || result.Usage.InputTokens != 3 {
		t.Fatalf("refusal result=%+v err=%v", result, err)
	}
	result, err = (&Anthropic{Client: clientWith(http.StatusOK, `{}`, nil)}).NextTurn(context.Background(), providerRequest())
	if err != nil || result.Usage.TokensReported || result.Usage.CachedInputTokens != nil || result.Usage.Requests != 1 {
		t.Fatalf("usage fabricated: %+v err=%v", result.Usage, err)
	}
}

func TestAnthropicRejectsInvalidToolSchema(t *testing.T) {
	request := providerRequest()
	request.Tools[0].InputSchema = json.RawMessage(`[`)
	if _, err := (&Anthropic{Client: clientWith(http.StatusOK, `{}`, nil)}).NextTurn(context.Background(), request); err == nil {
		t.Fatal("invalid tool schema was sent")
	}
}
