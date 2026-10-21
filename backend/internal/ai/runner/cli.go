// Executes supported model CLIs in isolated sessions with scoped tools, bounded output,
// and provider-specific result parsing.

package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/agentview"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/cliisolation"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/prompts"
)

const disableFlag = "--disable"

type CommandExecutor interface {
	Run(context.Context, string, []string, []string, string) ([]byte, error)
}
type processTimingKey struct{}
type processTiming struct{ StartMS *int64 }
type OSCommandExecutor struct{}

func (OSCommandExecutor) Run(ctx context.Context, name string, args, environment []string, directory string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = environment
	command.Dir = directory
	isolateCommand(command)
	command.WaitDelay = time.Second
	var output limitedOutput
	command.Stdout, command.Stderr = &output, &output
	if name == "claude" {
		// Claude JSON is one document. Never interleave stderr diagnostics
		// with that document, even when they have no trailing newline.
		command.Stderr = io.Discard
	}
	started := time.Now()
	err := command.Start()
	if err == nil {
		elapsed := time.Since(started).Milliseconds()
		trace.SpanFromContext(ctx).SetAttributes(attribute.Int64("battlefield.cli_process_start_ms", elapsed))
		if timing, ok := ctx.Value(processTimingKey{}).(*processTiming); ok {
			timing.StartMS = &elapsed
		}
		err = command.Wait()
	}
	return output.Bytes(), err
}

type CLIRunner struct {
	PrefetchContext bool
	ReasoningEffort string
	Provider        string
	Model           string
	Executor        CommandExecutor
	Connector       MCPConnector
}

func (r CLIRunner) Run(ctx context.Context, session ai.DecisionSession) (result ai.DecisionResult, runErr error) {
	ctx, span := generationSpan(ctx, session, "cli_session")
	timing := &processTiming{}
	ctx = context.WithValue(ctx, processTimingKey{}, timing)
	delivery := r.contextDelivery(session)
	span.SetAttributes(attribute.String("battlefield.context_delivery", delivery))
	defer func() { recordCLIExecutionMetadata(&result, timing, delivery) }()
	defer func() { finishGeneration(span, result.Usage, runErr != nil || !result.Accepted) }()
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
	invocation, category, err := r.prepareInvocation(ctx, session, temporary, workspace)
	if err != nil {
		return ai.DecisionResult{ErrorCategory: category}, err
	}
	span.SetAttributes(attribute.Int("battlefield.prompt.bytes", len(invocation.prompt)))
	output, err := executor.Run(ctx, cliisolation.Binary(r.Provider), invocation.args, invocation.environment, workspace)
	if r.Provider == cliisolation.ProviderAntigravity {
		result, runErr = antigravityResult(output)
	} else if r.Provider == cliisolation.ProviderClaude {
		result, runErr = claudeResult(output)
	} else {
		result, runErr = codexResult(output)
	}
	if err != nil {
		result.Accepted = false
		result.ErrorCategory = "cli"
		return result, err
	}
	return result, runErr
}

type cliInvocation struct {
	args, environment []string
	prompt            string
}

func (r CLIRunner) prepareInvocation(ctx context.Context, session ai.DecisionSession, temporary, workspace string) (cliInvocation, string, error) {
	prompt, err := r.sessionPrompt(ctx, session)
	if err != nil {
		return cliInvocation{}, "mcp_context", err
	}
	var args, environment []string
	switch r.Provider {
	case cliisolation.ProviderCodex:
		args = []string{"exec", "--json", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only", "--ignore-user-config", "-C", workspace,
			disableFlag, "shell_tool", disableFlag, "browser_use", disableFlag, "in_app_browser", disableFlag, "computer_use", disableFlag, "apps", disableFlag, "image_generation", disableFlag, "multi_agent", disableFlag, "skill_search", disableFlag, "skill_mcp_dependency_install", disableFlag, "plugins", disableFlag, "hooks", disableFlag, "goals", disableFlag, "standalone_web_search",
			"-c", `approval_policy="never"`, "-c", `mcp_servers.battlefield.url="` + session.MCPURL + `"`,
			"-c", `mcp_servers.battlefield.env_http_headers={Authorization="BATTLEFIELD_MCP_AUTHORIZATION"}`,
			"-c", `mcp_servers.battlefield.enabled_tools=["` + strings.Join(prompts.SessionToolNames(session), `","`) + `"]`,
			"-c", `mcp_servers.battlefield.default_tools_approval_mode="approve"`, prompt}
		args = withModel(args, r.Model)
		if r.ReasoningEffort != "" {
			args = append([]string{"-c", `model_reasoning_effort="` + r.ReasoningEffort + `"`}, args...)
		}
		environment = replaceEnvironment(cliisolation.CodexEnvironment(), "CODEX_WORKSPACE", workspace)
		environment = replaceEnvironment(environment, "BATTLEFIELD_MCP_AUTHORIZATION", "Bearer "+session.CapabilityToken)
	case cliisolation.ProviderClaude:
		args, err = claudeArguments(temporary, prompt, r.Model, session)
		if err != nil {
			return cliInvocation{}, "cli_isolation", err
		}
		if r.ReasoningEffort != "" {
			args = append([]string{"--effort", r.ReasoningEffort}, args...)
		}
		environment = cliisolation.ClaudeEnvironment()
	case cliisolation.ProviderAntigravity:
		environment, err = cliisolation.PrepareAntigravity(temporary, session.MCPURL, session.CapabilityToken, prompts.SessionToolNames(session)...)
		if err != nil {
			return cliInvocation{}, "cli_isolation", err
		}
		args = withModel([]string{"--print", prompt, "--output-format", "json", "--disable-slash-commands", "--print-timeout", "2m"}, r.Model)

	default:
		return cliInvocation{}, "cli_provider", fmt.Errorf("unsupported CLI provider %q", r.Provider)
	}
	return cliInvocation{args: args, environment: environment, prompt: prompt}, "", nil
}

func (r CLIRunner) contextDelivery(session ai.DecisionSession) string {
	if r.Provider == cliisolation.ProviderAntigravity || (r.PrefetchContext && session.JobType != "faction_background") {
		return "prefetch"
	}
	return "tool"
}

func (r CLIRunner) sessionPrompt(ctx context.Context, session ai.DecisionSession) (string, error) {
	var payload json.RawMessage
	if r.contextDelivery(session) == "prefetch" {
		var err error
		payload, err = r.prefetchContext(ctx, session)
		if err != nil {
			return "", err
		}
	}
	return prompts.SessionSystem(session) + "\n" + prompts.SessionDecision(session, payload), nil
}

func recordCLIExecutionMetadata(result *ai.DecisionResult, timing *processTiming, delivery string) {
	if result.ProviderData == nil {
		result.ProviderData = map[string]any{}
	}
	result.ProviderData["contextDelivery"] = delivery
	if timing.StartMS != nil {
		result.ProviderData["cliProcessStartMs"] = *timing.StartMS
	}
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
			Input  *int64 `json:"input_tokens"`
			Output *int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	for _, line := range strings.Split(string(output), "\n") {
		var envelope resultEnvelope
		if json.Unmarshal([]byte(line), &envelope) == nil && envelope.Status != "" {
			usage := ai.Usage{Scope: "cli_session", CLIInvocations: 1, Requests: 1}
			if envelope.Usage.Input != nil && envelope.Usage.Output != nil {
				usage.InputTokens = *envelope.Usage.Input
				usage.OutputTokens = *envelope.Usage.Output
				usage.TokensReported = true
			}
			if len(envelope.DeniedActions) > 0 {
				return ai.DecisionResult{ErrorCategory: "cli_permission", Usage: usage}, fmt.Errorf("Antigravity denied a required tool action")
			}
			if envelope.Status != "SUCCESS" {
				return ai.DecisionResult{ErrorCategory: "cli", Usage: usage}, fmt.Errorf("Antigravity returned %s", envelope.Status)
			}
			return ai.DecisionResult{Accepted: true, Usage: usage}, nil
		}
	}
	return ai.DecisionResult{ErrorCategory: "cli", Usage: ai.Usage{Scope: "cli_session", CLIInvocations: 1, Requests: 1}}, fmt.Errorf("Antigravity returned no terminal result")
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

// Fetch the scoped observation with our SDK for inline delivery. Antigravity
// requires this because it offloads larger MCP responses to unreadable local files.
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
	payload, _, err := client.Call(ctx, ai.ToolCall{Name: prompts.SessionToolNames(session)[0], Arguments: arguments})
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
	return agentview.SessionContextPayload(session, payload), nil
}

// Bound allocation during execution, including output from a compromised CLI.
type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	const limit = 4 << 20
	if b.Len()+len(p) > limit {
		return 0, io.ErrShortBuffer
	}
	return b.Buffer.Write(p)
}
