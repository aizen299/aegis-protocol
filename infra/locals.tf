locals {
  name = "${var.project}-${var.environment}"

  tags = merge(var.tags, {
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "terraform"
  })

  # Secrets live in SSM Parameter Store as SecureString, never in tfvars or task definitions.
  ssm_prefix = "/${var.project}/${var.environment}"
}
