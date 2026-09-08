variable "name" { type = string }
variable "vpc_id" { type = string }
variable "private_subnet_ids" { type = list(string) }
variable "ingress_sg_id" { type = string }
variable "node_type" { type = string }
variable "tags" { type = map(string) }

variable "engine_version" {
  type    = string
  default = "7.1"
}
