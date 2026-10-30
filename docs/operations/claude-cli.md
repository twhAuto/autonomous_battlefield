# Claude subscription CLI

The `claude-subscription-local` profile runs the `claude-cli` provider on the host,
using Claude Code **2.1.267** locally and the **sonnet** model alias.
It is selectable through
the same model catalog as Codex and Antigravity, with existing role restrictions.

1. Install Claude Code 2.1.267 for local development using the [official installation instructions](https://code.claude.com/docs/en/setup),
   and turn off its automatic updates (`"autoUpdates": false` in `~/.claude.json`).
   The profile accepts only a CLI whose `claude --version` output starts with the
   profile's `supportedVersionPrefix` (`"2.1.267 "`); any other version, newer
   included, leaves the profile unavailable as "claude CLI version is not supported".
   Raise it only through the CLI upgrade described below.
2. Run `claude auth login` and sign in with a Claude subscription. The worker checks
   `claude auth status --json` for a saved `claude.ai` login. API-key and Console logins do
   not satisfy this profile. Authentication stays on the host in its native location.
3. Start the dev bridge with `./scripts/dev/run-k8s-cli-agent.sh claude`. Keep it running while
   battles or faction-generation jobs use the profile. `auto` detects all available
   CLIs, `all` requires all three, and `both` retains Codex plus Antigravity.

The launcher toggles `CLAUDE_CLI_PROFILE_AVAILABLE` in the cluster while connected,
then clears it on exit. Keep its environment-file default `false`. Production
uses the existing dedicated agent VM and requires
`BATTLEFIELD_CLI_ISOLATION_VERIFIED=true` before advertising any CLI profile.
Sign the VM worker in with the [provider login steps](cli-worker-verification.md#provider-login).
Do not install or mount Claude credentials inside the API or AI gateway containers.

The runner uses [Claude's CLI restrictions](https://code.claude.com/docs/en/cli-reference):
restricted mode, no built-in tools, disabled hooks and slash commands, no personal
settings sources, strict MCP configuration, no session persistence, and only the
job-specific battlefield MCP tools approved without prompts. The request capability
is stored in a mode-0600 temporary file, never command arguments, and deleted on
exit. The subprocess environment excludes API keys and application secrets.
Host managed policies can still make tools unavailable; permission denials fail
execution rather than being bypassed. The worker requires a validated MCP
submission, not merely a successful CLI exit or a textual plan.

Battle setup also offers `haiku` for lighter workloads, and `opus` and `fable` for
more demanding work. Haiku uses model-default effort so it works with older supported
CLI versions whose Haiku model does not expose effort settings; Sonnet, Opus and
Fable offer low/medium/high effort. The default is `sonnet`.

The model aliases follow the account's current model versions. To pin a model,
update `backend/config.yaml`, `infra/deploy/base/infrastructure/configmap.yaml`,
`infra/deploy/agent-vm/config.yaml`, `infra/deploy/agent-vm/managed-settings.json`,
and `scripts/dev/run-k8s-cli-agent.sh` consistently.
When upgrading the production CLI, verify restricted-mode behavior and update the
version pin in those same files.

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

If AI gateway forwarding fails, the launcher prints the `kubectl port-forward` error
before removing temporary files. It waits up to 120 seconds for the AI gateway deployment
rollout before starting forwarding. On the development workstation, choose an
unused loopback port, for example `HOST_AI_GATEWAY_PORT=28084 ./scripts/dev/run-k8s-cli-agent.sh claude`
only when 28084 is free. The production server already uses that port for its
CLI VM bridge; do not stop it to run a development launcher. A second agent also needs
a distinct `HOST_AGENT_HEALTH_PORT`. When tracing is enabled, set distinct
`HOST_OTEL_PORT` and `HOST_OTEL_HEALTH_PORT` too. For pod startup failures, inspect
`kubectl -n battlefield get pods -l app.kubernetes.io/component=ai-gateway` using the
registered dev kubeconfig and context. Health probes bypass HTTP proxies and
check that the forwarding process is still alive.

The pinned CLI's `auth status --help` confirms JSON is the default; the worker
requests `--json` explicitly. A live synthetic check on 2.1.267 verified both
subscription authentication and MCP submission with `--tools ""` (which removes
built-in tools, not MCP tools). Claude stdout is parsed as one JSON document,
including multiline output; stderr is discarded so diagnostics cannot corrupt it.
The host-agent health wait allows 60 seconds of retry delays for both serial
15-second subscription login probes, with individually bounded HTTP probes.

Personal instruction isolation is a separate concern from tool permissions:
the native login retains `HOME`, and this integration does not explicitly disable
user-level `~/.claude/CLAUDE.md` memory. Do not assume personal instructions are
excluded by the settings flags. Use a dedicated host account without personal
memory for reproducible comparisons. The supplied agent-VM configuration leaves
Claude disabled until an operator deliberately enables it after isolation checks.
