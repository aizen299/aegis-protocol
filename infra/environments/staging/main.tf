terraform {
  required_version = ">= 1.9.0"

  backend "s3" {
    bucket         = "aegis-terraform-state"
    key            = "staging/terraform.tfstate"
    region         = "us-east-1"
    dynamodb_table = "aegis-terraform-locks"
    encrypt        = true
  }
}

provider "aws" {
  region = var.region

  default_tags {
    tags = { Project = "aegis", Environment = "staging" }
  }
}

module "aegis" {
  source = "../../"

  environment       = "staging"
  region            = var.region
  db_instance_class = var.db_instance_class
  redis_node_type   = var.redis_node_type
  api_desired_count = var.api_desired_count
}
