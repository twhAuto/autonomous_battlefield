# Deployment policy

[`kubernetes/workload.rego`](kubernetes/workload.rego) contains the Conftest checks
for rendered Kubernetes workloads. The
[security launcher](../../../scripts/ci/security-and-policy.sh) runs them during CI;
[supply-chain assets](../supply-chain/README.md) own image-admission configuration.

An exception is valid only when a reviewed Notion ticket records its owner, exact
resource/rule, justification, compensating control, expiry date and removal plan.
Link that ticket beside the narrow exception. Permanent, repository-wide or
undocumented bypasses are rejected.
