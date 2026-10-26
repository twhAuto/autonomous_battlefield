package runner

import (
	"strings"
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

func TestCodexFailureMessagesAreClassified(t *testing.T) {
	for _, tc := range []struct{ output, category, detail string }{
		{"{\"type\":\"error\",\"message\":\"exceeded retry limit, last status: 429 Too Many Requests\"}\n{\"type\":\"turn.failed\",\"error\":{\"message\":\"exceeded retry limit, last status: 429 Too Many Requests\"}}", ErrorCategoryProviderRateLimited, "429 Too Many Requests"},
		{`{"type":"error","message":"You've hit your usage limit."}`, ErrorCategoryProviderRateLimited, "usage limit"},
		{`{"type":"turn.failed","error":{"message":"stream disconnected before completion"}}`, "cli", "Codex failed: stream disconnected before completion"},
		{`{"type":"turn.failed"}`, "cli", "no successful terminal turn"},
	} {
		result, err := codexResult([]byte(tc.output))
		if err == nil || result.Accepted || result.ErrorCategory != tc.category || !strings.Contains(err.Error(), tc.detail) {
			t.Fatalf("output=%s result=%+v err=%v", tc.output, result, err)
		}
	}
}

func TestCLIFailureDetailIsBoundedAndRedacted(t *testing.T) {
	detail := CLIFailureDetail("first line\n  Authorization: bearer abc.def\t" + strings.Repeat("x", 400))
	if strings.Contains(detail, "abc.def") || strings.ContainsAny(detail, "\n\t") || !strings.HasPrefix(detail, "first line Authorization: Bearer [redacted] ") || len([]rune(detail)) != cliFailureDetailRunes+1 {
		t.Fatalf("detail=%q", detail)
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
