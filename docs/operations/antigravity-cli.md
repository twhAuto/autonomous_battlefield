# Antigravity subscription agent

The Google subscription profile uses `agy` (Antigravity CLI), replacing the retired Gemini CLI provider. Its persistent profile ID remains `gemini-subscription-local`, and its availability flag remains `GEMINI_CLI_PROFILE_AVAILABLE`, preserving existing battle configuration references. The provider is now `antigravity-cli` and the display name is Antigravity / Google subscription.

## WSL setup

1. Install Antigravity CLI using the [official installation instructions](https://antigravity.google/docs/cli/install/). The configured release family is 1.1.x; this migration was developed against 1.1.27. Check `agy --version`. The host launcher includes `~/.local/bin` on PATH.
2. Run `agy` interactively and complete Google sign-in. The worker supports the host OS keyring (with an accessible Linux secret service/session D-Bus connection) and Antigravity’s native `~/.gemini/antigravity-cli/antigravity-oauth-token` fallback file. An API key or the old Gemini OAuth file does not satisfy this check.
3. Run `./deploy-local.sh` to update the dev backend and profile configuration.
4. In another terminal run `./run-k8s-cli-agent.sh antigravity`, or use `both` for Codex and Antigravity. Keep this terminal open during battles.
5. Select the Antigravity subscription profile in the battle form. Verify a decision at a planning tick submits a strategy and reports usage.

The pinned model is `gemini-3.7-flash-medium`. Model access depends on the signed-in account; consult `agy models` when diagnosing availability. Change the model consistently in `backend/config.yaml`, `deploy/base/infrastructure/configmap.yaml`, and the host launcher if selecting another supported model.

## Isolation and troubleshooting

Each invocation gets a temporary home, private MCP configuration, and permissions allowing only the two battlefield MCP tools while denying file, command, and URL operations. Personal settings, unrelated MCP servers, and provider API keys are not inherited. Authentication comes from the host keyring or a mode-0600 temporary copy of the native token file. The copy is removed with the isolated home when the invocation ends and is never written back to the host login or placed in Kubernetes. See the official [headless](https://antigravity.google/docs/cli/headless/), [permissions](https://antigravity.google/docs/cli/permissions/), and [MCP](https://antigravity.google/docs/cli/mcp/) documentation.

Before enabling the profile, the launcher runs a bounded account-usage probe under the same isolation. If this fails, confirm interactive sign-in in the same WSL session. A native token file must be a regular file, not a symlink. Temporary token refreshes are not persisted back to the host; if the saved login expires, sign in again with `agy`. CLI execution errors and unsuccessful or missing result envelopes fail the decision. Account authentication does not by itself establish model access or successful MCP submission.

Restart the bridge after deployment or CLI configuration changes. Ctrl+C removes the advertised availability. Production does not need an Antigravity installation or subscription login: this is a host development bridge, and no server deployment is performed by installing it.

The runner deliberately omits `toolPermission: strict`: CLI 1.1.27 blocks explicitly allowed MCP calls in that mode. The exact MCP allowlist and file, shell, and URL deny rules remain enforced. A result containing `denied_actions` fails as `cli_permission`, even if the CLI reports SUCCESS. The MCP completion endpoint also requires an accepted database plan; completion without one becomes `plan_not_submitted`, allowing immediate fallback instead of waiting for the decision timeout. Redeploy dev and restart the host bridge after this change.

CLI context responses include explicit target-tick and version metadata alongside the full structured context. CLI prompts require copying those values and making a substantive tactical or production decision. Name-only CLI plans are rejected; after three invalid submissions the decision is failed so the battle can fall back. MCP logs record context byte counts and validation field/code pairs without dumping observations or credentials. Usage reporting retries transient failures up to three times.

For an opt-in real CLI regression check using only synthetic in-memory battle data, run from `backend`:

```bash
PATH="$HOME/.local/bin:$PATH" BATTLEFIELD_LIVE_AGY=1 go test ./internal/ai/mcpserver -run '^TestAntigravityLiveDecision$' -v -count=1
```

This uses the installed CLI and its subscription allowance. Normal test runs skip it. It verifies a substantive submission for the exact context tick through the application MCP server; it does not deploy or alter existing battles.

Antigravity CLI offloads larger MCP tool responses to local files. Because battlefield CLI sessions deny file reads, the host now fetches the request-scoped context through the MCP SDK and embeds the validated JSON directly in the Antigravity prompt (maximum 64 KiB). The model submits through MCP without fetching the large response again. This keeps existing file restrictions and avoids blind tick guessing when the CLI hides an offloaded response.
