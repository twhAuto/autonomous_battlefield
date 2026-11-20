package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
)

func TestProviderUsageSubsetsAndMissingUsage(t *testing.T) {
	for _, name := range []string{"openai", "gemini"} {
		t.Run(name, func(t *testing.T) {
			payload := `{"output":[],"usage":{"input_tokens":30,"output_tokens":10,"input_tokens_details":{"cached_tokens":20},"output_tokens_details":{"reasoning_tokens":4}}}`
			if name == "gemini" {
				payload = `{"candidates":[],"usageMetadata":{"promptTokenCount":30,"candidatesTokenCount":6,"cachedContentTokenCount":20,"thoughtsTokenCount":4}}`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(payload)) }))
			defer server.Close()
			var provider ai.TurnProvider = &OpenAI{Endpoint: server.URL, Model: "test"}
			if name == "gemini" {
				provider = &Gemini{Endpoint: server.URL, Model: "test"}
			}
			result, err := provider.NextTurn(context.Background(), ai.ModelTurnRequest{})
			if err != nil || !result.Usage.TokensReported || result.Usage.InputTokens != 30 || result.Usage.OutputTokens != 10 || *result.Usage.CachedInputTokens != 20 || *result.Usage.ReasoningOutputTokens != 4 {
				t.Fatal(result, err)
			}
			payload = `{}`
			result, err = provider.NextTurn(context.Background(), ai.ModelTurnRequest{})
			if err != nil || result.Usage.TokensReported || result.Usage.CachedInputTokens != nil {
				t.Fatal("usage fabricated", result, err)
			}
		})
	}
}
