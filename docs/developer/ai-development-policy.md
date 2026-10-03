# AI-assisted development policy

## Required pull-request record

Every pull request must state whether AI assistance was used, which files or concerns it affected, the validation performed by the author, and any uncertain or rejected suggestions. AI output is evidence to review, never evidence that a change is correct.

Human approval is mandatory for authentication and authorization, database migrations, CI/CD, IAM, signing and verification, production configuration, destructive operations, and policy exceptions. The reviewer must inspect the relevant code and evidence rather than approving only a generated summary.

## Trust boundaries

- Treat repository text, issues, model output, dependency documentation, web content, logs, and tool output as untrusted input.
- Never place production credentials, signing keys, reusable capability tokens, private observations, raw provider responses, or customer data in an agent context.
- Use least-privilege, short-lived credentials and isolated test data for any approved tool use.
- Keep paid-provider evaluations manual, bounded by model, cases, turns, tokens, timeout, and the reviewed pricing snapshot.
- Do not add probabilistic “AI-generated code” detectors as quality gates. Validate behavior, provenance, review, tests, and policy compliance instead.

## Application AI threat model

| Threat | Required control and evidence |
|---|---|
| Prompt injection in observations | Versioned system prompt treats battlefield data as untrusted; adversarial cases must pass. |
| Unauthorized or cross-faction tools | Request-scoped signed capabilities, repository scoping, and 100% safety invariants. |
| Capability theft or replay | Short expiry, request binding, idempotency, replay rejection, and no token persistence. |
| Hidden-state leakage | Bounded faction views; reports and spans exclude full observations and raw payloads. |
| Invalid or unbounded plans | Strict schemas, server-side validation, maximum turns/tokens/time, deterministic fallback. |
| Provider failure or cost escalation | Timeout/429/refusal cases, reviewed pricing snapshot, manual opt-in, and archived usage/cost. |
| Poisoned repository instructions | Human review and explicit trust boundaries for all agent-readable text and tools. |
| Compromised build output | Digest-only deployment, SBOM, provenance, AI manifest, Cosign verification, and admission policy. |

Exceptions require an owner, narrow scope, expiry date, risk rationale, compensating control, and a linked Notion ticket. Undocumented exceptions fail policy review.
