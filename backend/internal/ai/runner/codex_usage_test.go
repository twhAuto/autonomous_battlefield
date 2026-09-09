package runner

import (
	"testing"
)

func TestCodexUsageEvents(t *testing.T) {
	result, err := codexResult([]byte(`diagnostic
{"type":"item.completed","item":{"type":"reasoning","text":"private-marker"}}
{"type":"turn.completed","usage":{"input_tokens":30,"cached_input_tokens":20,"output_tokens":10,"reasoning_output_tokens":4}}`))
	if err != nil || !result.Accepted || !result.Usage.TokensReported || result.Usage.InputTokens != 30 || result.Usage.OutputTokens != 10 || *result.Usage.CachedInputTokens != 20 || *result.Usage.ReasoningOutputTokens != 4 {
		t.Fatal(result, err)
	}
	if result.Usage.ModelCalls != nil || result.Usage.CLIInvocations != 1 || result.Usage.Scope != "cli_session" || len(result.ProviderData) != 0 {
		t.Fatal("invented model count or leaked reasoning", result)
	}
	for _, output := range []string{"", "malformed", `{"type":"turn.failed"}`, "{\"type\":\"turn.completed\"}\n{\"type\":\"turn.failed\"}"} {
		if result, err := codexResult([]byte(output)); err == nil || result.Accepted {
			t.Fatal("invalid terminal success", output)
		}
	}
	result, err = codexResult([]byte(`{"type":"turn.completed"}`))
	if err != nil || result.Usage.TokensReported || result.Usage.CachedInputTokens != nil {
		t.Fatal("missing usage invented", result, err)
	}
	result, err = codexResult([]byte(`{"type":"turn.completed","usage":{"input_tokens":0,"output_tokens":0}}`))
	if err != nil || !result.Usage.TokensReported {
		t.Fatal("reported zero lost")
	}
}

func TestCLIUsageSurvivesPermissionFailure(t *testing.T) {
	result, err := antigravityResult([]byte(`{"status":"ERROR","denied_actions":[{}],"usage":{"input_tokens":30,"output_tokens":2}}`))
	if err == nil || result.Usage.InputTokens != 30 || !result.Usage.TokensReported {
		t.Fatal(result, err)
	}
	result, err = antigravityResult([]byte(`{"status":"SUCCESS"}`))
	if err != nil || result.Usage.TokensReported || result.Usage.ModelCalls != nil {
		t.Fatal("missing usage invented")
	}
}
