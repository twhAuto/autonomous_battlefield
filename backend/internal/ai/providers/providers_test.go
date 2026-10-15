// Checks provider tool-call and usage decoding, refusal/error mapping, and HTTP response
// cleanup.

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

func providerRequest() ai.ModelTurnRequest {
	return ai.ModelTurnRequest{SystemPrompt: "system", Messages: []ai.Message{{Role: "user", Content: "decide"}}, Tools: []ai.ToolDefinition{{Name: "get_decision_context", Description: "context", InputSchema: []byte(`{"type":"object","additionalProperties":false,"required":["requestId"],"properties":{"requestId":{"type":"string"},"guidanceOffset":{"type":"integer"}},"x-extension":"preserved"}`)}}, MaxTokens: 100}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
func clientWith(status int, body string, check func(*http.Request)) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if check != nil {
			check(request)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
}

func TestOpenAIParsesToolCallsAndUsage(t *testing.T) {
	client := clientWith(http.StatusOK, `{"output":[{"type":"function_call","call_id":"call-1","name":"get_decision_context","arguments":"{\"requestId\":\"r\"}"}],"usage":{"input_tokens":12,"output_tokens":4}}`, func(request *http.Request) {
		var body struct {
			Instructions string           `json:"instructions"`
			Input        []map[string]any `json:"input"`
			Tools        []struct {
				Strict     *bool          `json:"strict"`
				Parameters map[string]any `json:"parameters"`
			} `json:"tools"`
		}
		{
			err := json.NewDecoder(request.Body).Decode(&body)
			if err != nil || body.Instructions != "system" || len(body.Input) != 1 || body.Input[0]["role"] != "user" || body.Input[0]["content"] != "decide" {
				t.Fatalf("instructions=%q input=%v err=%v", body.Instructions, body.Input, err)
			}
		}
		if request.Header.Get("Authorization") != "Bearer key" {
			t.Error("missing authorization")
		}
		if len(body.Tools) != 1 {
			t.Fatalf("tools=%+v", body.Tools)
		}
		if body.Tools[0].Strict != nil {
			t.Fatal("MCP schema must not be advertised as strict")
		}
		if body.Tools[0].Parameters["x-extension"] != "preserved" {
			t.Fatalf("parameters=%+v", body.Tools[0].Parameters)
		}
	})
	result, err := (&OpenAI{APIKey: "key", Model: "model", Client: client}).NextTurn(context.Background(), providerRequest())
	if err != nil || len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "call-1" || result.Usage.InputTokens != 12 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestProvidersRecognizeRefusals(t *testing.T) {
	openAI := clientWith(http.StatusOK, `{"output":[{"type":"message","content":[{"type":"refusal","refusal":"no"}]}]}`, nil)
	{
		_, err := (&OpenAI{Client: openAI}).NextTurn(context.Background(), providerRequest())
		if !errors.Is(err, ErrRefusal) {
			t.Fatalf("OpenAI refusal error = %v", err)
		}
	}
	gemini := clientWith(http.StatusOK, `{"candidates":[{"finishReason":"SAFETY","content":{"parts":[]}}]}`, nil)
	{
		_, err := (&Gemini{Client: gemini}).NextTurn(context.Background(), providerRequest())
		if !errors.Is(err, ErrRefusal) {
			t.Fatalf("Gemini refusal error = %v", err)
		}
	}
}

func TestGeminiParsesToolCallsAndErrors(t *testing.T) {
	client := clientWith(http.StatusOK, `{"candidates":[{"content":{"parts":[{"text":"working"},{"functionCall":{"name":"submit_strategy_plan","args":{"requestId":"r"}}}]}}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":3}}`, func(request *http.Request) {
		checkGeminiRequest(t, request)
	})
	result, err := (&Gemini{APIKey: "key", Model: "model", Client: client}).NextTurn(context.Background(), providerRequest())
	if err != nil || result.Text != "working" || len(result.ToolCalls) != 1 || result.Usage.OutputTokens != 3 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	failure := clientWith(http.StatusTooManyRequests, "no", nil)
	{
		_, err := (&Gemini{Client: failure}).NextTurn(context.Background(), providerRequest())
		if !errors.Is(err, ErrRateLimit) || !strings.Contains(err.Error(), "429") {
			t.Fatal(err)
		}
	}
	{
		_, err := (&OpenAI{Client: failure}).NextTurn(context.Background(), providerRequest())
		if !errors.Is(err, ErrRateLimit) {
			t.Fatalf("OpenAI rate-limit error = %v", err)
		}
	}
}

// geminiRequest is the part of a Gemini generateContent request the tests inspect.
type geminiRequest struct {
	SystemInstruction struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"systemInstruction"`
	Contents []struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"contents"`
	Tools []struct {
		FunctionDeclarations []struct {
			Parameters           json.RawMessage `json:"parameters"`
			ParametersJSONSchema map[string]any  `json:"parametersJsonSchema"`
		} `json:"functionDeclarations"`
	} `json:"tools"`
	ToolConfig struct {
		FunctionCallingConfig struct {
			Mode string `json:"mode"`
		} `json:"functionCallingConfig"`
	} `json:"toolConfig"`
}

// checkGeminiRequest requires the system prompt, message, tool schema, forced function
// calling and API key to reach Gemini in its own request format.
func checkGeminiRequest(t *testing.T, request *http.Request) {
	t.Helper()
	var body geminiRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.SystemInstruction.Parts) != 1 || body.SystemInstruction.Parts[0].Text != "system" || len(body.Contents) != 1 || len(body.Contents[0].Parts) != 1 || body.Contents[0].Parts[0].Text != "decide" {
		t.Fatalf("unexpected request: %+v", body)
	}
	if len(body.Tools) != 1 || len(body.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools=%+v", body.Tools)
	}
	declaration := body.Tools[0].FunctionDeclarations[0]
	if len(declaration.Parameters) != 0 {
		t.Fatal("Gemini typed parameters field must not receive arbitrary JSON Schema")
	}
	if declaration.ParametersJSONSchema["x-extension"] != "preserved" {
		t.Fatalf("parametersJsonSchema=%+v", declaration.ParametersJSONSchema)
	}
	if body.ToolConfig.FunctionCallingConfig.Mode != "ANY" {
		t.Fatalf("function calling mode=%q", body.ToolConfig.FunctionCallingConfig.Mode)
	}
	if request.Header.Get("x-goog-api-key") != "key" {
		t.Error("missing key")
	}
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

func TestProvidersCloseResponsesAndPreserveErrors(t *testing.T) {
	for name, create := range map[string]func(*http.Client) ai.TurnProvider{
		"OpenAI": func(client *http.Client) ai.TurnProvider { return &OpenAI{Client: client} },
		"Gemini": func(client *http.Client) ai.TurnProvider { return &Gemini{Client: client} },
	} {
		for _, test := range []struct {
			name   string
			status int
			body   string
		}{
			{"success", 200, `{}`},
			{"rate limit", 429, `private provider detail`},
			{"server error", 503, `private provider detail`},
			{"malformed JSON", 200, `{`},
		} {
			t.Run(name+"/"+test.name, func(t *testing.T) {
				body := &trackedBody{Reader: strings.NewReader(test.body)}
				_, err := create(jsonPostClient(t, test.status, body)).NextTurn(context.Background(), providerRequest())
				if !body.closed {
					t.Fatal("response body was not closed")
				}
				checkProviderError(t, name, test.status, test.name == "success", err)
			})
		}
		t.Run(name+"/cancellation", func(t *testing.T) {
			checkProviderCancellation(t, create)
		})
	}
}

// jsonPostClient answers a JSON POST with status and body, and fails any other request.
func jsonPostClient(t *testing.T, status int, body io.ReadCloser) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected request: %s %v", request.Method, request.Header)
		}
		return &http.Response{StatusCode: status, Body: body, Header: make(http.Header)}, nil
	})}
}

// checkProviderError requires an error exactly when the call should fail, keeping the
// rate-limit identity and status but never the provider's response body.
func checkProviderError(t *testing.T, provider string, status int, success bool, err error) {
	t.Helper()
	if (err == nil) != success {
		t.Fatalf("error=%v", err)
	}
	if status == 429 && !errors.Is(err, ErrRateLimit) {
		t.Fatalf("rate limit identity lost: %v", err)
	}
	if status == 503 && err.Error() != provider+" response status 503" {
		t.Fatalf("unexpected error: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "private provider detail") {
		t.Fatalf("response body leaked: %v", err)
	}
}

func checkProviderCancellation(t *testing.T, create func(*http.Client) ai.TurnProvider) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	})}
	if _, err := create(client).NextTurn(ctx, providerRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation identity lost: %v", err)
	}
}

func TestProviderErrorsReportKindWithoutMessage(t *testing.T) {
	for name, test := range map[string]struct {
		provider ai.TurnProvider
		body     string
		want     string
	}{
		"OpenAI": {&OpenAI{}, `{"error":{"message":"echoed request content","type":"invalid_request_error","code":"invalid_value"}}`, "OpenAI response status 400 (invalid_request_error/invalid_value)"},
		"Gemini": {&Gemini{}, `{"error":{"code":400,"message":"echoed request content","status":"INVALID_ARGUMENT"}}`, "Gemini response status 400 (INVALID_ARGUMENT/400)"},
		"unsafe": {&OpenAI{}, `{"error":{"type":"has spaces and\nnewlines","code":"ok_code"}}`, "OpenAI response status 400 (ok_code)"},
	} {
		t.Run(name, func(t *testing.T) {
			switch provider := test.provider.(type) {
			case *OpenAI:
				provider.Client = clientWith(http.StatusBadRequest, test.body, nil)
			case *Gemini:
				provider.Client = clientWith(http.StatusBadRequest, test.body, nil)
			}
			_, err := test.provider.NextTurn(context.Background(), providerRequest())
			if err == nil || err.Error() != test.want || strings.Contains(err.Error(), "echoed") {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestProvidersRejectOversizedResponses(t *testing.T) {
	huge := `{"output":[],"padding":"` + strings.Repeat("x", maxResponseBytes) + `"}`
	_, err := (&OpenAI{Client: clientWith(http.StatusOK, huge, nil)}).NextTurn(context.Background(), providerRequest())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response accepted: %v", err)
	}
}

func TestGeminiEscapesModelInPath(t *testing.T) {
	var path string
	client := clientWith(http.StatusOK, `{"candidates":[]}`, func(request *http.Request) { path = request.URL.EscapedPath() })
	_, _ = (&Gemini{Model: "gemini/../../other?x=1", Client: client}).NextTurn(context.Background(), providerRequest())
	if path != "/v1beta/models/gemini%2F..%2F..%2Fother%3Fx=1:generateContent" {
		t.Fatalf("model not escaped in path: %s", path)
	}
}

// conversationRequest is a second turn: the model fetched context and a unit detail,
// and both results came back.
func conversationRequest() ai.ModelTurnRequest {
	request := providerRequest()
	request.Messages = append(request.Messages,
		ai.Message{Role: "assistant", Content: "checking", ToolCalls: []ai.ToolCall{
			{ID: "call-a", Name: "get_decision_context", Arguments: json.RawMessage(`{"requestId":"r"}`), Signature: "sig-a"},
			{ID: "call-b", Name: "get_unit_details", Arguments: json.RawMessage(`{"requestId":"r","unitIds":["ranger"]}`)},
		}},
		ai.Message{Role: "tool", ToolCallID: "call-a", Name: "get_decision_context", Content: `{"context":{"requestId":"r"}}`},
		ai.Message{Role: "tool", ToolCallID: "call-b", Name: "get_unit_details", Content: `not an object`},
	)
	return request
}

func TestOpenAISendsNativeConversationItems(t *testing.T) {
	var input []map[string]any
	client := clientWith(http.StatusOK, `{"output":[]}`, func(request *http.Request) {
		var body struct {
			Input []map[string]any `json:"input"`
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		input = body.Input
	})
	_, _ = (&OpenAI{Client: client}).NextTurn(context.Background(), conversationRequest())
	want := []map[string]any{
		{"role": "user", "content": "decide"},
		{"role": "assistant", "content": "checking"},
		{"type": "function_call", "call_id": "call-a", "name": "get_decision_context", "arguments": `{"requestId":"r"}`},
		{"type": "function_call", "call_id": "call-b", "name": "get_unit_details", "arguments": `{"requestId":"r","unitIds":["ranger"]}`},
		{"type": "function_call_output", "call_id": "call-a", "output": `{"context":{"requestId":"r"}}`},
		{"type": "function_call_output", "call_id": "call-b", "output": "not an object"},
	}
	got, _ := json.Marshal(input)
	expected, _ := json.Marshal(want)
	if string(got) != string(expected) {
		t.Fatalf("input=%s\nwant  %s", got, expected)
	}
}

func TestGeminiSendsNativeTurnsAndEchoesSignatures(t *testing.T) {
	var contents json.RawMessage
	client := clientWith(http.StatusOK, `{"candidates":[]}`, func(request *http.Request) {
		var body struct {
			Contents json.RawMessage `json:"contents"`
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		contents = body.Contents
	})
	_, _ = (&Gemini{Client: client}).NextTurn(context.Background(), conversationRequest())
	want := `[{"parts":[{"text":"decide"}],"role":"user"},` +
		`{"parts":[{"text":"checking"},{"functionCall":{"args":{"requestId":"r"},"name":"get_decision_context"},"thoughtSignature":"sig-a"},{"functionCall":{"args":{"requestId":"r","unitIds":["ranger"]},"name":"get_unit_details"}}],"role":"model"},` +
		`{"parts":[{"functionResponse":{"name":"get_decision_context","response":{"context":{"requestId":"r"}}}},{"functionResponse":{"name":"get_unit_details","response":{"result":"not an object"}}}],"role":"user"}]`
	if string(contents) != want {
		t.Fatalf("contents=%s\nwant     %s", contents, want)
	}
}

func TestGeminiKeepsThoughtSignatures(t *testing.T) {
	client := clientWith(http.StatusOK, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_decision_context","args":{"requestId":"r"}},"thoughtSignature":"opaque"}]}}]}`, nil)
	result, err := (&Gemini{Client: client}).NextTurn(context.Background(), providerRequest())
	if err != nil || len(result.ToolCalls) != 1 || result.ToolCalls[0].Signature != "opaque" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
