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
