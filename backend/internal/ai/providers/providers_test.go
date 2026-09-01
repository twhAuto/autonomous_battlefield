package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

func providerRequest() ai.ModelTurnRequest {
	return ai.ModelTurnRequest{SystemPrompt: "system", Messages: []ai.Message{{Role: "user", Content: "decide"}}, Tools: []ai.ToolDefinition{{Name: "get_decision_context", Description: "context", InputSchema: []byte(`{"type":"object"}`)}}, MaxTokens: 100}
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
		if request.Header.Get("Authorization") != "Bearer key" {
			t.Error("missing authorization")
		}
	})
	result, err := (&OpenAI{APIKey: "key", Model: "model", Client: client}).NextTurn(context.Background(), providerRequest())
	if err != nil || len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "call-1" || result.Usage.InputTokens != 12 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestProvidersRecognizeRefusals(t *testing.T) {
	openAI := clientWith(http.StatusOK, `{"output":[{"type":"message","content":[{"type":"refusal","refusal":"no"}]}]}`, nil)
	if _, err := (&OpenAI{Client: openAI}).NextTurn(context.Background(), providerRequest()); !errors.Is(err, ErrRefusal) {
		t.Fatalf("OpenAI refusal error = %v", err)
	}
	gemini := clientWith(http.StatusOK, `{"candidates":[{"finishReason":"SAFETY","content":{"parts":[]}}]}`, nil)
	if _, err := (&Gemini{Client: gemini}).NextTurn(context.Background(), providerRequest()); !errors.Is(err, ErrRefusal) {
		t.Fatalf("Gemini refusal error = %v", err)
	}
}

func TestGeminiParsesToolCallsAndErrors(t *testing.T) {
	client := clientWith(http.StatusOK, `{"candidates":[{"content":{"parts":[{"text":"working"},{"functionCall":{"name":"submit_strategy_plan","args":{"requestId":"r"}}}]}}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":3}}`, func(request *http.Request) {
		if request.Header.Get("x-goog-api-key") != "key" {
			t.Error("missing key")
		}
	})
	result, err := (&Gemini{APIKey: "key", Model: "model", Client: client}).NextTurn(context.Background(), providerRequest())
	if err != nil || result.Text != "working" || len(result.ToolCalls) != 1 || result.Usage.OutputTokens != 3 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	failure := clientWith(http.StatusTooManyRequests, "no", nil)
	if _, err := (&Gemini{Client: failure}).NextTurn(context.Background(), providerRequest()); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatal(err)
	}
	if _, err := (&OpenAI{Client: failure}).NextTurn(context.Background(), providerRequest()); err == nil {
		t.Fatal("OpenAI status accepted")
	}
}
