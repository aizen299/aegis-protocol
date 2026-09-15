variable "name" { type = string }
variable "vpc_id" { type = string }
variable "private_subnet_ids" { type = list(string) }
variable "ingress_sg_id" { type = string }
variable "instance_class" { type = string }
variable "multi_az" { type = bool }
variable "tags" { type = map(string) }

variable "engine_version" {
  type    = string
  default = "16.6"
}

variable "allocated_storage" {
  type    = number
  default = 50
}

variable "max_allocated_storage" {
  type    = number
  default = 500
}

variable "backup_retention_days" {
  type    = number
  default = 7
}

# RDS computes max_connections from the instance class in the default parameter group, so this must
# be kept in step with instance_class. Stated explicitly rather than derived: a wrong value produces
# an alarm that never fires, which is indistinguishable from a healthy database.
variable "max_connections" {
  description = "Maximum connections the instance class allows; the connection alarm fires at 80% of it"
  type        = number
  default     = 340 # db.t4g.micro under the default parameter group
}
