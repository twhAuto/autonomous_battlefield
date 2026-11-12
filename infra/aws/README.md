# AWS reference environment

This Terraform root creates the cost budget first, then a two-AZ VPC, private EKS nodes, immutable ECR repositories, encrypted RDS PostgreSQL with managed credentials and backups, Secrets Manager placeholders, cluster control-plane logs, an OIDC provider and scoped IRSA roles, the CloudWatch observability add-on, and the AWS Load Balancer Controller.

It intentionally does not store secret values. Populate application secrets through an approved secret-management workflow and create narrowly scoped IRSA roles for additional controllers. The EKS API endpoint is private; run Terraform/Helm from an approved VPC-connected runner or establish the reviewed administrative network path before apply.

This is a reference implementation; it has not been applied to an AWS account by
this repository. Before provisioning, verify billing, protect the root account
with MFA, and use temporary role-based credentials for daily administration.
Budget alerts are notifications, not spending limits. EKS control-plane, compute,
database, load-balancer, networking and storage charges require their own review;
Free Tier eligibility does not make the environment free. See
[EKS pricing](https://aws.amazon.com/eks/pricing/) and
[AWS Budgets](https://docs.aws.amazon.com/cost-management/latest/userguide/create-cost-budget.html).

```bash
cp terraform.tfvars.example terraform.tfvars
terraform init
terraform plan -out plan.tfplan
terraform apply plan.tfplan
```

RDS deletion protection is enabled and final snapshots are mandatory. A complete teardown therefore requires an explicit, reviewed change that disables deletion protection and chooses a unique final snapshot identifier.

Images are never rebuilt for AWS. Run `scripts/cd/promote-to-ecr.sh` after the GHCR
digest, signature, SBOM, provenance and AI manifest have passed verification. The
promotion copies OCI referrers and verifies the unchanged subject digest; see
[AWS promotion limits](../../docs/operations/jenkins.md#aws-promotion-limits).

Before treating this environment as production-ready, review the account-specific
plan and IAM permissions, populate secrets, configure workload identity and
application ingress, and point `DATABASE_URL` at private RDS with TLS. Run migrations
before application rollout, then verify secret rotation, backup restoration, pod
disruption, failed-rollout recovery and teardown. Keep the application metric
contract and use the [observability requirements](../../docs/operations/observability.md#storage-and-access-by-environment)
for durable, authenticated monitoring.
