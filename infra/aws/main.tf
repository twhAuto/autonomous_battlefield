locals {
  name = "autonomous-battlefield-${var.environment}"
  tags = {
    Project     = "autonomous-battlefield"
    Environment = var.environment
    ManagedBy   = "terraform"
  }
  azs = ["${var.aws_region}a", "${var.aws_region}b"]
}

resource "aws_budgets_budget" "monthly" {
  name         = "${local.name}-monthly"
  budget_type  = "COST"
  limit_amount = tostring(var.monthly_budget_usd)
  limit_unit   = "USD"
  time_unit    = "MONTHLY"

  dynamic "notification" {
    for_each = toset([50, 80, 100])
    content {
      comparison_operator        = "GREATER_THAN"
      threshold                  = notification.value
      threshold_type             = "PERCENTAGE"
      notification_type          = "FORECASTED"
      subscriber_email_addresses = [var.budget_email]
    }
  }
}

resource "aws_vpc" "this" {
  cidr_block           = "10.42.0.0/16"
  enable_dns_hostnames = true
  enable_dns_support   = true
  tags                 = { Name = local.name }
  depends_on           = [aws_budgets_budget.monthly]
}

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id
  tags   = { Name = local.name }
}

resource "aws_subnet" "public" {
  for_each                = { for index, az in local.azs : az => index }
  vpc_id                  = aws_vpc.this.id
  availability_zone       = each.key
  cidr_block              = cidrsubnet(aws_vpc.this.cidr_block, 8, each.value)
  map_public_ip_on_launch = false
  tags = {
    Name                     = "${local.name}-public-${each.key}"
    "kubernetes.io/role/elb" = "1"
  }
}

resource "aws_subnet" "private" {
  for_each          = { for index, az in local.azs : az => index }
  vpc_id            = aws_vpc.this.id
  availability_zone = each.key
  cidr_block        = cidrsubnet(aws_vpc.this.cidr_block, 8, each.value + 10)
  tags = {
    Name                              = "${local.name}-private-${each.key}"
    "kubernetes.io/role/internal-elb" = "1"
  }
}

resource "aws_eip" "nat" {
  domain     = "vpc"
  depends_on = [aws_internet_gateway.this]
}

resource "aws_nat_gateway" "this" {
  allocation_id = aws_eip.nat.id
  subnet_id     = values(aws_subnet.public)[0].id
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.this.id
  }
}

resource "aws_route_table" "private" {
  vpc_id = aws_vpc.this.id
  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.this.id
  }
}

resource "aws_route_table_association" "public" {
  for_each       = aws_subnet.public
  subnet_id      = each.value.id
  route_table_id = aws_route_table.public.id
}

resource "aws_route_table_association" "private" {
  for_each       = aws_subnet.private
  subnet_id      = each.value.id
  route_table_id = aws_route_table.private.id
}

resource "aws_ecr_repository" "images" {
  for_each             = toset(["backend", "frontend"])
  name                 = "autonomous-battlefield-${each.key}"
  image_tag_mutability = "IMMUTABLE"
  encryption_configuration { encryption_type = "AES256" }
  image_scanning_configuration { scan_on_push = true }
  depends_on = [aws_budgets_budget.monthly]
}

resource "aws_ecr_lifecycle_policy" "images" {
  for_each   = aws_ecr_repository.images
  repository = each.value.name
  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Retain the latest 30 promoted non-release subjects"
      selection = {
        tagStatus     = "tagged"
        tagPrefixList = ["sha-"]
        countType     = "imageCountMoreThan"
        countNumber   = 30
      }
      action = { type = "expire" }
    }]
  })
}

data "aws_iam_policy_document" "eks_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["eks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "eks" {
  name               = "${local.name}-eks"
  assume_role_policy = data.aws_iam_policy_document.eks_assume.json
}

resource "aws_iam_role_policy_attachment" "eks" {
  role       = aws_iam_role.eks.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"
}

resource "aws_kms_key" "eks" {
  description             = "${local.name} Kubernetes secret envelope encryption"
  deletion_window_in_days = 30
  enable_key_rotation     = true
  depends_on              = [aws_budgets_budget.monthly]
}

resource "aws_kms_alias" "eks" {
  name          = "alias/${local.name}-eks-secrets"
  target_key_id = aws_kms_key.eks.key_id
}

resource "aws_eks_cluster" "this" {
  name     = local.name
  role_arn = aws_iam_role.eks.arn
  version  = var.kubernetes_version
  vpc_config {
    subnet_ids              = values(aws_subnet.private)[*].id
    endpoint_private_access = true
    endpoint_public_access  = false
  }
  encryption_config {
    resources = ["secrets"]
    provider {
      key_arn = aws_kms_key.eks.arn
    }
  }
  enabled_cluster_log_types = ["api", "audit", "authenticator", "controllerManager", "scheduler"]
  depends_on                = [aws_iam_role_policy_attachment.eks]
}

data "aws_iam_policy_document" "nodes_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "nodes" {
  name               = "${local.name}-nodes"
  assume_role_policy = data.aws_iam_policy_document.nodes_assume.json
}

resource "aws_iam_role_policy_attachment" "nodes" {
  for_each = toset([
    "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
    "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
    "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"
  ])
  role       = aws_iam_role.nodes.name
  policy_arn = each.value
}

resource "aws_eks_node_group" "system" {
  cluster_name    = aws_eks_cluster.this.name
  node_group_name = "system"
  node_role_arn   = aws_iam_role.nodes.arn
  subnet_ids      = values(aws_subnet.private)[*].id
  instance_types  = var.node_instance_types
  capacity_type   = "ON_DEMAND"
  scaling_config {
    desired_size = 2
    min_size     = 2
    max_size     = 4
  }
  update_config {
    max_unavailable = 1
  }
  depends_on = [aws_iam_role_policy_attachment.nodes]
}

resource "aws_security_group" "database" {
  name        = "${local.name}-database"
  description = "PostgreSQL from private VPC workloads"
  vpc_id      = aws_vpc.this.id
  ingress {
    description = "PostgreSQL from the private VPC"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = [aws_vpc.this.cidr_block]
  }
}

resource "aws_db_subnet_group" "this" {
  name       = local.name
  subnet_ids = values(aws_subnet.private)[*].id
}

resource "aws_db_instance" "postgres" {
  identifier                  = local.name
  engine                      = "postgres"
  engine_version              = "17"
  instance_class              = "db.t4g.micro"
  allocated_storage           = 20
  max_allocated_storage       = 100
  storage_encrypted           = true
  db_name                     = var.database_name
  username                    = var.database_username
  manage_master_user_password = true
  db_subnet_group_name        = aws_db_subnet_group.this.name
  vpc_security_group_ids      = [aws_security_group.database.id]
  publicly_accessible         = false
  multi_az                    = false
  backup_retention_period     = 7
  deletion_protection         = true
  skip_final_snapshot         = false
  final_snapshot_identifier   = "${local.name}-final"
  apply_immediately           = false
  depends_on                  = [aws_budgets_budget.monthly]
}

resource "aws_secretsmanager_secret" "application" {
  for_each                = toset(["auth", "ai-providers", "mcp-capability"])
  name                    = "${local.name}/${each.key}"
  recovery_window_in_days = 30
  depends_on              = [aws_budgets_budget.monthly]
}

data "tls_certificate" "eks" {
  url = aws_eks_cluster.this.identity[0].oidc[0].issuer
}

resource "aws_iam_openid_connect_provider" "eks" {
  url             = aws_eks_cluster.this.identity[0].oidc[0].issuer
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.eks.certificates[0].sha1_fingerprint]
}

locals {
  oidc_issuer = replace(aws_eks_cluster.this.identity[0].oidc[0].issuer, "https://", "")
}

data "aws_iam_policy_document" "cloudwatch_assume" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.eks.arn]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_issuer}:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_issuer}:sub"
      values   = ["system:serviceaccount:amazon-cloudwatch:cloudwatch-agent"]
    }
  }
}

resource "aws_iam_role" "cloudwatch" {
  name               = "${local.name}-cloudwatch"
  assume_role_policy = data.aws_iam_policy_document.cloudwatch_assume.json
}

resource "aws_iam_role_policy_attachment" "cloudwatch" {
  for_each = toset([
    "arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy",
    "arn:aws:iam::aws:policy/AWSXrayWriteOnlyAccess"
  ])
  role       = aws_iam_role.cloudwatch.name
  policy_arn = each.value
}

resource "aws_eks_addon" "core" {
  for_each = toset(["vpc-cni", "coredns", "kube-proxy", "eks-pod-identity-agent"])

  cluster_name                = aws_eks_cluster.this.name
  addon_name                  = each.value
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "PRESERVE"
  depends_on                  = [aws_eks_node_group.system]
}

resource "aws_eks_addon" "cloudwatch" {
  cluster_name                = aws_eks_cluster.this.name
  addon_name                  = "amazon-cloudwatch-observability"
  service_account_role_arn    = aws_iam_role.cloudwatch.arn
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "PRESERVE"
  depends_on                  = [aws_iam_role_policy_attachment.cloudwatch, aws_eks_node_group.system]
}

data "aws_iam_policy_document" "load_balancer_assume" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.eks.arn]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_issuer}:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_issuer}:sub"
      values   = ["system:serviceaccount:kube-system:aws-load-balancer-controller"]
    }
  }
}

resource "aws_iam_role" "load_balancer" {
  name               = "${local.name}-load-balancer"
  assume_role_policy = data.aws_iam_policy_document.load_balancer_assume.json
}

data "aws_iam_policy_document" "load_balancer" {
  statement {
    actions = [
      "ec2:AuthorizeSecurityGroupIngress", "ec2:CreateSecurityGroup", "ec2:CreateTags",
      "ec2:DeleteSecurityGroup", "ec2:Describe*", "ec2:RevokeSecurityGroupIngress",
      "elasticloadbalancing:*", "iam:CreateServiceLinkedRole",
      "wafv2:AssociateWebACL", "wafv2:DisassociateWebACL", "wafv2:GetWebACLForResource",
      "shield:CreateProtection", "shield:DeleteProtection", "shield:DescribeProtection"
    ]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "load_balancer" {
  name   = "controller"
  role   = aws_iam_role.load_balancer.id
  policy = data.aws_iam_policy_document.load_balancer.json
}

resource "helm_release" "aws_load_balancer_controller" {
  name       = "aws-load-balancer-controller"
  repository = "https://aws.github.io/eks-charts"
  chart      = "aws-load-balancer-controller"
  namespace  = "kube-system"
  version    = "1.13.0"

  set = [
    { name = "clusterName", value = aws_eks_cluster.this.name },
    { name = "region", value = var.aws_region },
    { name = "vpcId", value = aws_vpc.this.id },
    { name = "serviceAccount.create", value = "true" },
    { name = "serviceAccount.name", value = "aws-load-balancer-controller" },
    { name = "serviceAccount.annotations.eks\\.amazonaws\\.com/role-arn", value = aws_iam_role.load_balancer.arn }
  ]

  depends_on = [aws_iam_role_policy.load_balancer, aws_eks_node_group.system]
}
