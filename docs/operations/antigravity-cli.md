# Antigravity subscription agent

The Google subscription profile runs `agy` (Antigravity CLI) with the
`antigravity-cli` provider, shown as Antigravity / Google subscription. Its profile
ID stays `gemini-subscription-local` and its availability flag
`GEMINI_CLI_PROFILE_AVAILABLE`, from the retired Gemini CLI provider, so existing
battle configurations keep working.

## WSL setup

1. Install Antigravity CLI using the
   [official installation instructions](https://antigravity.google/docs/cli/install/).
   The configured release family is 1.2.x; check `agy --version`. The host
   launcher includes `~/.local/bin` on PATH.
2. Run `agy` interactively and complete Google sign-in. The worker supports the
   host OS keyring (with an accessible Linux secret service/session D-Bus
   connection) and Antigravity's native
   `~/.gemini/antigravity-cli/antigravity-oauth-token` fallback file. An API key or
   the old Gemini OAuth file does not satisfy this check.
3. Run `./scripts/cd/deploy-local.sh` to update the dev backend and profile
   configuration.
4. In another terminal run `./scripts/dev/run-k8s-cli-agent.sh antigravity`, or use
   `both` for Codex and Antigravity. Keep this terminal open during battles.
5. Select the Antigravity subscription profile in the battle form. Verify that a
   decision at a planning tick submits a strategy and reports usage.

The pinned model is `gemini-3.8-flash-medium`. Model access depends on the signed-in
account; consult `agy models` when diagnosing availability. Change the model
consistently in `backend/config.yaml`,
`infra/deploy/base/infrastructure/configmap.yaml`,
`infra/deploy/agent-vm/managed-settings.json` (applied to the CLI VM by
[`battlefield-platform/cli`](cli-worker.md)), and the host launcher if selecting
another supported model. A retired model fails the decision within seconds with
error category `cli` and no tokens.

## Isolation and troubleshooting

See [CLI runtime](../architecture/ai-decisions.md#cli-runtime) for isolation,
context delivery, and submission enforcement.

Before enabling the profile, the launcher runs a bounded account-usage probe under
the same isolation. If this fails, confirm interactive sign-in in the same WSL
session. A native token file must be a regular file, not a symlink. Temporary token
refreshes are not persisted back to the host; if the saved login expires, sign in
again with `agy`. CLI execution errors and unsuccessful or missing result envelopes
fail the decision. Account authentication does not by itself establish model access
or successful MCP submission.

Restart the bridge after deployment or CLI configuration changes. Ctrl+C removes the
advertised availability. Installing it on a workstation deploys nothing to a
server. The production CLI VM defines the same profile, disabled until an operator
installs the CLI, signs in and passes the
[isolation gate](cli-worker-verification.md#isolation-gate).

### Missing plans

`plan_not_submitted` means the CLI finished without a plan the AI gateway accepted;
it does not by itself mean the decision deadline expired. The SDK can reject a
malformed tool call before the plan handler runs. The gateway then logs
`decision_tool_input_rejected` with the authenticated request ID, the missing
required field names and whether a nested `plan` wrapper was sent, never argument
values or raw error text. Workers are instructed to submit one flat
tool-arguments object with every required field, using `{}`/`[]` rather than
omitting unused objects and arrays. A written plan or a CLI `SUCCESS` result is not
enough: the MCP tool must return `accepted=true`, within the existing
three-submission limit. When a runner fails before the deadline, the fallback keeps
its failure category; decisions still unresolved at the deadline record `timeout`.

After changing embedded prompts, rebuild and restart the host agent; after backend
changes, redeploy. For dev, run `./scripts/cd/deploy-local.sh`, then restart
`./scripts/dev/run-k8s-cli-agent.sh` before starting a new battle.

A `cli_permission` failure means the result reported a denied action; verify the
installed version against the configured prefix before restarting the bridge.

For an opt-in real CLI regression check using only synthetic in-memory battle data,
run from `backend`:

```bash
PATH="$HOME/.local/bin:$PATH" BATTLEFIELD_LIVE_AGY=1 go test ./internal/ai/mcpserver -run '^TestAntigravityLiveDecision$' -v -count=1
```

This uses the installed CLI and its subscription allowance. Normal test runs skip
it. It verifies a substantive submission for the exact context tick through the
application MCP server; it does not deploy or alter existing battles.
