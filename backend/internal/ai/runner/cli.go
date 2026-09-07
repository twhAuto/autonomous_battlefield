// Executes supported model CLIs in isolated sessions with scoped tools, bounded output,
// and provider-specific result parsing.

package runner

import (
	"context"
	"encoding/json"
	"fmt"

	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/clienv"
)

const disableFlag = "--disable"

type CommandExecutor interface {
	Run(context.Context, string, []string, []string, string) ([]byte, error)
}
type OSCommandExecutor struct{}

func (OSCommandExecutor) Run(ctx context.Context, name string, args, environment []string, directory string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = environment
	command.Dir = directory
	return command.CombinedOutput()
}

type CLIRunner struct {
	Provider  string
	Model     string
	Executor  CommandExecutor
	Connector MCPConnector
}

func (r CLIRunner) Run(ctx context.Context, session ai.DecisionSession) (ai.DecisionResult, error) {
	temporary, err := os.MkdirTemp("", "battlefield-agent-")
	if err != nil {
		return ai.DecisionResult{ErrorCategory: "cli_isolation"}, err
	}
	defer os.RemoveAll(temporary)
	workspace := filepath.Join(temporary, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		return ai.DecisionResult{}, err
	}
	executor := r.Executor
	if executor == nil {
		executor = OSCommandExecutor{}
	}
	prompt := ai.SessionSystemPrompt(session) + "\n" + ai.SessionDecisionPrompt(session, nil)
	var name string
	var args, environment []string
	switch r.Provider {
	case "codex-cli":
		name = "codex"
		args = []string{"exec", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only", "--ignore-user-config", "-C", workspace,
			disableFlag, "shell_tool", disableFlag, "browser_use", disableFlag, "in_app_browser", disableFlag, "computer_use", disableFlag, "apps", disableFlag, "image_generation", disableFlag, "multi_agent", disableFlag, "skill_search", disableFlag, "skill_mcp_dependency_install", disableFlag, "plugins", disableFlag, "hooks", disableFlag, "goals", disableFlag, "standalone_web_search",
			"-c", `approval_policy="never"`, "-c", `mcp_servers.battlefield.url="` + session.MCPURL + `"`,
			"-c", `mcp_servers.battlefield.env_http_headers={Authorization="BATTLEFIELD_MCP_AUTHORIZATION"}`,
			"-c", `mcp_servers.battlefield.enabled_tools=["` + strings.Join(ai.SessionToolNames(session), `","`) + `"]`,
			"-c", `mcp_servers.battlefield.default_tools_approval_mode="approve"`, prompt}
		args = withModel(args, r.Model)
		environment = replaceEnvironment(os.Environ(), "CODEX_WORKSPACE", workspace)
		environment = replaceEnvironment(environment, "BATTLEFIELD_MCP_AUTHORIZATION", "Bearer "+session.CapabilityToken)
	case "antigravity-cli":
		name = "agy"
		payload, contextErr := r.prefetchContext(ctx, session)
		if contextErr != nil {
			return ai.DecisionResult{ErrorCategory: "mcp_context"}, contextErr
		}
		prompt = ai.SessionSystemPrompt(session) + "\n" + ai.SessionDecisionPrompt(session, payload)

		environment, err = clienv.PrepareAntigravity(temporary, session.MCPURL, session.CapabilityToken, ai.SessionToolNames(session)...)
		if err != nil {
			return ai.DecisionResult{ErrorCategory: "cli_isolation"}, err
		}
		args = withModel([]string{"--print", prompt, "--output-format", "json", "--disable-slash-commands", "--print-timeout", "2m"}, r.Model)

	default:
		return ai.DecisionResult{ErrorCategory: "cli_provider"}, fmt.Errorf("unsupported CLI provider %q", r.Provider)
	}
	output, err := executor.Run(ctx, name, args, environment, workspace)
	if err != nil {
		return ai.DecisionResult{ErrorCategory: "cli", ProviderData: map[string]any{"exitOutput": redactedCLIOutput(output, session.CapabilityToken)}}, err
	}
	if r.Provider == "antigravity-cli" {
		return antigravityResult(output)
	}
	return ai.DecisionResult{Accepted: true, Usage: ai.Usage{Requests: 1}}, nil
}

func withModel(arguments []string, model string) []string {
	model = strings.TrimSpace(model)
	if model == "" {
		return arguments
	}
	return append([]string{"--model", model}, arguments...)
}

// Combined output can include diagnostic lines before the single JSON envelope.
func antigravityResult(output []byte) (ai.DecisionResult, error) {
	type resultEnvelope struct {
		Status        string            `json:"status"`
		DeniedActions []json.RawMessage `json:"denied_actions"`
		Usage         struct {
			Input  int64 `json:"input_tokens"`
			Output int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	for _, line := range strings.Split(string(output), "\n") {
		var envelope resultEnvelope
		if json.Unmarshal([]byte(line), &envelope) == nil && envelope.Status != "" {
			if len(envelope.DeniedActions) > 0 {
				return ai.DecisionResult{ErrorCategory: "cli_permission"}, fmt.Errorf("Antigravity denied a required tool action")
			}
			if envelope.Status != "SUCCESS" {
				return ai.DecisionResult{ErrorCategory: "cli"}, fmt.Errorf("Antigravity returned %s", envelope.Status)
			}
			return ai.DecisionResult{Accepted: true, Usage: ai.Usage{Requests: 1, InputTokens: envelope.Usage.Input, OutputTokens: envelope.Usage.Output}}, nil
		}
	}
	return ai.DecisionResult{ErrorCategory: "cli"}, fmt.Errorf("Antigravity returned no terminal result")
}

func replaceEnvironment(values []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(values)+1)
	for _, item := range values {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}

func redactedCLIOutput(output []byte, token string) string {
	if token != "" {
		output = []byte(strings.ReplaceAll(string(output), token, "[redacted]"))
	}
	return boundedOutput(output)
}

func boundedOutput(value []byte) string {
	const limit = 2048
	if len(value) > limit {
		value = value[len(value)-limit:]
	}
	return strings.TrimSpace(string(value))
}

// Antigravity offloads larger MCP responses to local files. Fetch the scoped
// observation with our SDK and place it directly in the prompt instead.
func (r CLIRunner) prefetchContext(ctx context.Context, session ai.DecisionSession) (json.RawMessage, error) {
	connector := r.Connector
	if connector == nil {
		connector = MCPClient{}
	}
	client, err := connector.Connect(ctx, session.MCPURL, session.CapabilityToken)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	arguments, _ := json.Marshal(map[string]string{"requestId": session.RequestID})
	payload, _, err := client.Call(ctx, ai.ToolCall{Name: ai.SessionToolNames(session)[0], Arguments: arguments})
	if err != nil {
		return nil, err
	}
	if len(payload) > 64<<10 {
		return nil, fmt.Errorf("decision context exceeds 64 KiB prompt limit")
	}
	var result struct {
		Context ai.DecisionContext `json:"context"`
	}
	if json.Unmarshal(payload, &result) != nil || result.Context.RequestID != session.RequestID || (session.JobType != "faction_background" && ((result.Context.TargetTick == 0 && !result.Context.Opening) || result.Context.ExpectedVersion < 1)) {
		return nil, fmt.Errorf("invalid decision context metadata")
	}
	return payload, nil
}
