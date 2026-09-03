# AWS reference environment

This Terraform root creates the cost budget first, then a two-AZ VPC, private EKS nodes, immutable ECR repositories, encrypted RDS PostgreSQL with managed credentials and backups, Secrets Manager placeholders, cluster control-plane logs, an OIDC provider and scoped IRSA roles, the CloudWatch observability add-on, and the AWS Load Balancer Controller.

It intentionally does not store secret values. Populate application secrets through an approved secret-management workflow and create narrowly scoped IRSA roles for additional controllers. The EKS API endpoint is private; run Terraform/Helm from an approved VPC-connected runner or establish the reviewed administrative network path before apply.

```bash
cp terraform.tfvars.example terraform.tfvars
terraform init
terraform plan -out plan.tfplan
terraform apply plan.tfplan
```

RDS deletion protection is enabled and final snapshots are mandatory. A complete teardown therefore requires an explicit, reviewed change that disables deletion protection and chooses a unique final snapshot identifier.

Images are never rebuilt for AWS. Run `scripts/cd/promote-to-ecr.sh` after the GHCR digest, signature, SBOM, provenance, and AI manifest have passed verification.
