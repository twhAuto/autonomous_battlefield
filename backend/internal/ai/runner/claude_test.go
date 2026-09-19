package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

func TestClaudeResults(t *testing.T) {
	for _, tc := range []struct {
		name, output       string
		accepted, reported bool
		category           string
	}{
		{"success", `{"type":"result","subtype":"success","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":20,"cache_creation_input_tokens":3}}`, true, true, ""},
		{"missing usage", `{"type":"result","subtype":"success"}`, true, false, ""},
		{"zero usage", `{"type":"result","subtype":"success","usage":{"input_tokens":0,"output_tokens":0}}`, true, true, ""},
		{"negative usage", `{"type":"result","subtype":"success","usage":{"input_tokens":-1,"output_tokens":0}}`, true, false, ""},
		{"denied", `{"type":"result","subtype":"success","permission_denials":[{"tool_name":"test"}]}`, false, false, "cli_permission"},
		{"error", `{"type":"result","subtype":"success","is_error":true}`, false, false, "cli"},
		{"turn limit", `{"type":"result","subtype":"error_max_turns"}`, false, false, "cli"},
		{"missing", "diagnostic\n{}", false, false, "cli"},
		{"assistant text", `{"type":"assistant","subtype":"success"}`, false, false, "cli"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := claudeResult([]byte(tc.output))
			if result.Accepted != tc.accepted || (err == nil) != tc.accepted || result.Usage.TokensReported != tc.reported || result.ErrorCategory != tc.category {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if result.Usage.CLIInvocations != 1 || result.Usage.Scope != "cli_session" {
				t.Fatal(result.Usage)
			}
			if tc.name == "success" && (result.Usage.InputTokens != 33 || result.Usage.OutputTokens != 5 || result.Usage.CachedInputTokens == nil || *result.Usage.CachedInputTokens != 20) {
				t.Fatal(result.Usage)
			}
		})
	}
}

type claudeExecutor struct {
	t          *testing.T
	configPath string
	fail       bool
}

func (e *claudeExecutor) Run(_ context.Context, name string, args, environment []string, directory string) ([]byte, error) {
	e.t.Helper()
	if name != "claude" {
		e.t.Fatal(name)
	}
	flags := map[string]string{}
	for i := 0; i < len(args)-1; i++ {
		flags[args[i]] = args[i+1]
	}
	for key, value := range map[string]string{"--tools": "", "--model": "sonnet", "--permission-mode": "dontAsk", "--setting-sources": "", "--output-format": "json"} {
		if got, ok := flags[key]; !ok || got != value {
			e.t.Fatalf("flag %s=%q", key, got)
		}
	}
	for _, key := range []string{"--restricted", "--strict-mcp-config", "--disable-slash-commands", "--no-session-persistence"} {
		if _, ok := flags[key]; !ok {
			e.t.Fatal(key)
		}
	}
	if strings.Contains(strings.Join(args, " "), "secret-capability") || strings.Contains(strings.Join(environment, " "), "private-api-key") {
		e.t.Fatal("secret leaked")
	}
	if !strings.Contains(flags["--allowedTools"], "mcp__battlefield__submit_strategy_plan") {
		e.t.Fatal(flags)
	}
	e.configPath = flags["--mcp-config"]
	data, err := os.ReadFile(e.configPath)
	if err != nil {
		e.t.Fatal(err)
	}
	var config map[string]any
	if json.Unmarshal(data, &config) != nil || !strings.Contains(string(data), "Bearer secret-capability") {
		e.t.Fatal("missing MCP capability")
	}
	info, _ := os.Stat(e.configPath)
	if info.Mode().Perm() != 0o600 {
		e.t.Fatal(info.Mode())
	}
	if e.fail {
		return []byte(`{"type":"result","subtype":"success"}`), errors.New("exit")
	}
	return []byte(`{"type":"result","subtype":"success"}`), nil
}
func TestClaudeRunnerIsolationAndCleanup(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "private-api-key")
	for _, fail := range []bool{false, true} {
		executor := &claudeExecutor{t: t, fail: fail}
		result, err := (CLIRunner{Provider: "claude-cli", Model: "sonnet", Executor: executor}).Run(context.Background(), ai.DecisionSession{MCPURL: "http://localhost/mcp", CapabilityToken: "secret-capability"})
		if (err != nil) != fail || result.Accepted == fail {
			t.Fatalf("%+v %v", result, err)
		}
		if _, err := os.Stat(executor.configPath); !os.IsNotExist(err) {
			t.Fatalf("temporary MCP configuration remains: %v", err)
		}
	}
	if _, err := claudeArguments(t.TempDir()+"/missing", "prompt", "sonnet", ai.DecisionSession{}); err == nil {
		t.Fatal("expected write failure")
	}
}

func TestClaudeGenerationTools(t *testing.T) {
	args, err := claudeArguments(t.TempDir(), "prompt", "sonnet", ai.DecisionSession{JobType: "faction_background"})
	if err != nil {
		t.Fatal(err)
	}
	for i, arg := range args {
		if arg == "--allowedTools" {
			if args[i+1] != "mcp__battlefield__get_background_context,mcp__battlefield__submit_faction_background" {
				t.Fatal(args[i+1])
			}
			return
		}
	}
	t.Fatal("no tool allowlist")
}

func TestClaudePrettyJSONAndSeparatedDiagnostics(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	script := "#!/bin/sh\nprintf 'diagnostic without newline' >&2\nprintf '{\\n  \"type\": \"result\",\\n  \"subtype\": \"success\"\\n}\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	output, err := (OSCommandExecutor{}).Run(context.Background(), "claude", nil, os.Environ(), bin)
	if err != nil {
		t.Fatal(err)
	}
	result, err := claudeResult(output)
	if err != nil || !result.Accepted || strings.Contains(string(output), "diagnostic") {
		t.Fatalf("result=%+v error=%v output=%s", result, err, output)
	}
}
