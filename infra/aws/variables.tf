# Configurable region, environment, budget, database, and Kubernetes inputs for the AWS
# reference stack.

variable "aws_region" {
  type        = string
  description = "AWS region for the reference environment."
  default     = "ap-southeast-1"
}

variable "environment" {
  type        = string
  description = "Short environment name."
  default     = "dev"
  validation {
    condition     = can(regex("^[a-z0-9-]+$", var.environment))
    error_message = "environment must contain lowercase letters, numbers, or hyphens."
  }
}

variable "monthly_budget_usd" {
  type        = number
  description = "Monthly cost guardrail. Alerts are sent at 50, 80, and 100 percent."
  default     = 150
}

variable "budget_email" {
  type        = string
  description = "Email address that receives AWS Budget notifications."
}

variable "database_name" {
  type    = string
  default = "battlefield"
}

variable "database_username" {
  type      = string
  default   = "battlefield"
  sensitive = true
}

variable "kubernetes_version" {
  type    = string
  default = "1.33"
}

variable "node_instance_types" {
  type    = list(string)
  default = ["t3.medium"]
}
