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
		var body struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Input != "system\nUSER: decide\n" {
			t.Fatalf("transcript=%q err=%v", body.Input, err)
		}
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
		var body struct {
			Contents []struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"contents"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Contents) != 1 || len(body.Contents[0].Parts) != 1 || body.Contents[0].Parts[0].Text != "system\nUSER: decide\n" {
			t.Fatalf("unexpected transcript: %+v", body)
		}
		if request.Header.Get("x-goog-api-key") != "key" {
			t.Error("missing key")
		}
	})
	result, err := (&Gemini{APIKey: "key", Model: "model", Client: client}).NextTurn(context.Background(), providerRequest())
	if err != nil || result.Text != "working" || len(result.ToolCalls) != 1 || result.Usage.OutputTokens != 3 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	failure := clientWith(http.StatusTooManyRequests, "no", nil)
	if _, err := (&Gemini{Client: failure}).NextTurn(context.Background(), providerRequest()); !errors.Is(err, ErrRateLimit) || !strings.Contains(err.Error(), "429") {
		t.Fatal(err)
	}
	if _, err := (&OpenAI{Client: failure}).NextTurn(context.Background(), providerRequest()); !errors.Is(err, ErrRateLimit) {
		t.Fatalf("OpenAI rate-limit error = %v", err)
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
				client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/json" {
						t.Fatalf("unexpected request: %s %v", request.Method, request.Header)
					}
					return &http.Response{StatusCode: test.status, Body: body, Header: make(http.Header)}, nil
				})}
				_, err := create(client).NextTurn(context.Background(), providerRequest())
				if !body.closed {
					t.Fatal("response body was not closed")
				}
				if (err == nil) != (test.name == "success") {
					t.Fatalf("error=%v", err)
				}
				if test.status == 429 && !errors.Is(err, ErrRateLimit) {
					t.Fatalf("rate limit identity lost: %v", err)
				}
				if test.status == 503 && err.Error() != name+" response status 503" {
					t.Fatalf("unexpected error: %v", err)
				}
				if err != nil && strings.Contains(err.Error(), "private provider detail") {
					t.Fatalf("response body leaked: %v", err)
				}
			})
		}
		t.Run(name+"/cancellation", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return nil, request.Context().Err()
			})}
			if _, err := create(client).NextTurn(ctx, providerRequest()); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation identity lost: %v", err)
			}
		})
	}
}
