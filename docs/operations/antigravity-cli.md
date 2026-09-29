# Antigravity subscription agent

The Google subscription profile uses `agy` (Antigravity CLI), replacing the retired Gemini CLI provider. Its persistent profile ID remains `gemini-subscription-local`, and its availability flag remains `GEMINI_CLI_PROFILE_AVAILABLE`, preserving existing battle configuration references. The provider is now `antigravity-cli` and the display name is Antigravity / Google subscription.

## WSL setup

1. Install Antigravity CLI using the [official installation instructions](https://antigravity.google/docs/cli/install/). The configured release family is 1.2.x (verified with 1.2.0 by the live decision test below on 2026-09-29; the integration was originally developed against 1.1.27). Check `agy --version`. The host launcher includes `~/.local/bin` on PATH.
2. Run `agy` interactively and complete Google sign-in. The worker supports the host OS keyring (with an accessible Linux secret service/session D-Bus connection) and Antigravity’s native `~/.gemini/antigravity-cli/antigravity-oauth-token` fallback file. An API key or the old Gemini OAuth file does not satisfy this check.
3. Run `./deploy-local.sh` to update the dev backend and profile configuration.
4. In another terminal run `./run-k8s-cli-agent.sh antigravity`, or use `both` for Codex and Antigravity. Keep this terminal open during battles.
5. Select the Antigravity subscription profile in the battle form. Verify a decision at a planning tick submits a strategy and reports usage.

The pinned model is `gemini-3.7-flash-medium`. Model access depends on the signed-in account; consult `agy models` when diagnosing availability. Change the model consistently in `backend/config.yaml`, `deploy/base/infrastructure/configmap.yaml`, and the host launcher if selecting another supported model.

## Isolation and troubleshooting

See [CLI runtime](../design/system-architecture.md#cli-runtime) for isolation,
context delivery, and submission enforcement.

Before enabling the profile, the launcher runs a bounded account-usage probe under the same isolation. If this fails, confirm interactive sign-in in the same WSL session. A native token file must be a regular file, not a symlink. Temporary token refreshes are not persisted back to the host; if the saved login expires, sign in again with `agy`. CLI execution errors and unsuccessful or missing result envelopes fail the decision. Account authentication does not by itself establish model access or successful MCP submission.

Restart the bridge after deployment or CLI configuration changes. Ctrl+C removes the advertised availability. Production does not need an Antigravity installation or subscription login: this is a host development bridge, and no server deployment is performed by installing it.

For `plan_not_submitted` or malformed MCP arguments, use the
[missing-plan troubleshooting guide](ai-comparisons.md#troubleshooting-a-missing-antigravity-plan).
A `cli_permission` failure means the result reported a denied action; verify the
installed version against the configured prefix before restarting the bridge.

For an opt-in real CLI regression check using only synthetic in-memory battle data, run from `backend`:

```bash
PATH="$HOME/.local/bin:$PATH" BATTLEFIELD_LIVE_AGY=1 go test ./internal/ai/mcpserver -run '^TestAntigravityLiveDecision$' -v -count=1
```

This uses the installed CLI and its subscription allowance. Normal test runs skip it. It verifies a substantive submission for the exact context tick through the application MCP server; it does not deploy or alter existing battles.
