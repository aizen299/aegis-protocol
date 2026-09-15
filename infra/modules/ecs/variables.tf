variable "name" { type = string }
variable "region" { type = string }
variable "vpc_id" { type = string }
variable "public_subnet_ids" { type = list(string) }
variable "private_subnet_ids" { type = list(string) }
variable "service_sg_id" { type = string }
variable "alb_sg_id" { type = string }
variable "execution_role_arn" { type = string }
variable "api_task_role_arn" { type = string }
variable "indexer_role_arn" { type = string }
variable "ssm_prefix" { type = string }
variable "api_desired_count" { type = number }

# Sets APP_ENV on both tasks and the Environment dimension every alarm matches on. One value, so
# the two cannot drift into an alarm watching a dimension nothing emits.
variable "environment" { type = string }
variable "tags" { type = map(string) }

variable "image_tag" {
  description = "Immutable image tag. CI sets this to the commit SHA."
  type        = string
  default     = "latest"
}

variable "api_cpu" {
  type    = string
  default = "512"
}

variable "api_memory" {
  type    = string
  default = "1024"
}

variable "indexer_cpu" {
  type    = string
  default = "512"
}

variable "indexer_memory" {
  type    = string
  default = "1024"
}

variable "log_retention_days" {
  type    = number
  default = 30
}

# Provisional. docs/v1.0-production-plan.md §2.2 says the real threshold comes from an observed
# staging baseline; until then this is loose enough not to train anyone to ignore it.
variable "indexer_lag_alarm_seconds" {
  description = "Seconds of indexer lag before alarming"
  type        = number
  default     = 300
}
