# Claude subscription CLI

The `claude-subscription-local` profile runs the `claude-cli` provider on the host,
using Claude Code **2.1.267** and the **sonnet** model alias. It is selectable through
the same model catalog as Codex and Antigravity, with existing role restrictions.

1. Install Claude Code 2.1.267 using the [official installation instructions](https://code.claude.com/docs/en/setup).
2. Run `claude auth login` and sign in with a Claude subscription. The worker checks
   `claude auth status` for a saved `claude.ai` login. API-key and Console logins do
   not satisfy this profile. Authentication stays on the host in its native location.
3. Start the dev bridge with `./run-k8s-cli-agent.sh claude`. Keep it running while
   battles or faction-generation jobs use the profile. `auto` detects all available
   CLIs, `all` requires all three, and `both` retains Codex plus Antigravity.

The launcher toggles `CLAUDE_CLI_PROFILE_AVAILABLE` in the cluster while connected,
then clears it on exit. Keep its environment-file default `false`. Production
uses the existing dedicated agent VM and requires
`BATTLEFIELD_CLI_ISOLATION_VERIFIED=true` before advertising any CLI profile.
Do not install or mount Claude credentials inside the API or MCP containers.

The runner uses [Claude's CLI restrictions](https://code.claude.com/docs/en/cli-reference):
restricted mode, no built-in tools, disabled hooks and slash commands, no personal
settings sources, strict MCP configuration, no session persistence, and only the
job-specific battlefield MCP tools approved without prompts. The request capability
is stored in a mode-0600 temporary file, never command arguments, and deleted on
exit. The subprocess environment excludes API keys and application secrets.
Host managed policies can still make tools unavailable; permission denials fail
execution rather than being bypassed. The worker requires a validated MCP
submission, not merely a successful CLI exit or a textual plan.

The `sonnet` alias follows the account's current Sonnet model. To pin a model,
update `backend/config.yaml`, `deploy/base/infrastructure/configmap.yaml`,
`deploy/agent-vm/config.yaml`, and `scripts/dev/run-k8s-cli-agent.sh` consistently.
When upgrading the CLI, verify restricted-mode behavior and update the version
pin in those same files. The current pin prevents older CLIs without restricted
mode from being advertised.

A synthetic live check uses the existing subscription and makes model requests:

```bash
cd backend
BATTLEFIELD_LIVE_CLAUDE=1 go test ./internal/ai/mcpserver -run '^TestClaudeLiveDecision$' -count=1 -v
```

Ordinary tests use doubles and never invoke a live model. For login diagnostics
without inference, run `go run ./cmd/agent -check-claude-auth` from `backend`.
Terminal-result usage is one CLI-session aggregate: total input includes ordinary,
cache-read and cache-creation tokens; cached input is also recorded as a subset.
Missing usage stays unreported. Failed results, permission denials, timeouts and
invalid submissions use the existing worker failure and fallback behavior.

If MCP forwarding fails, the launcher prints the `kubectl port-forward` error
before removing temporary files. It waits up to 120 seconds for the MCP deployment
rollout before starting forwarding. For a port conflict, stop the existing bridge
or use `HOST_MCP_PORT=28084 ./run-k8s-cli-agent.sh claude`; a second agent also needs
a distinct `HOST_AGENT_HEALTH_PORT`. When tracing is enabled, set distinct
`HOST_OTEL_PORT` and `HOST_OTEL_HEALTH_PORT` too. For pod startup failures, inspect
`kubectl -n battlefield get pods -l app.kubernetes.io/component=mcp` using the
registered dev kubeconfig and context. Health probes bypass HTTP proxies and
check that the forwarding process is still alive.
