# Exports provisioned cluster, registry, database, secret, and IAM identifiers for
# application deployment and operations.

output "cluster_name" {
  value = aws_eks_cluster.this.name
}

output "ecr_repositories" {
  value = { for key, value in aws_ecr_repository.images : key => value.repository_url }
}

output "database_endpoint" {
  value = aws_db_instance.postgres.address
}

output "database_master_secret_arn" {
  value     = aws_db_instance.postgres.master_user_secret[0].secret_arn
  sensitive = true
}

output "application_secret_arns" {
  value = { for key, value in aws_secretsmanager_secret.application : key => value.arn }
}

output "oidc_provider_arn" {
  value = aws_iam_openid_connect_provider.eks.arn
}

output "load_balancer_controller_role_arn" {
  value = aws_iam_role.load_balancer.arn
}
