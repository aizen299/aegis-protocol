# Root composition. Environments under environments/ set the backend and pass tfvars; they do not
# declare resources of their own.

module "vpc" {
  source = "./modules/vpc"

  name     = local.name
  cidr     = var.vpc_cidr
  az_count = var.az_count
  tags     = local.tags
}

module "iam" {
  source = "./modules/iam"

  name       = local.name
  ssm_prefix = local.ssm_prefix
  region     = var.region
  tags       = local.tags
}

module "rds" {
  source = "./modules/rds"

  name               = local.name
  vpc_id             = module.vpc.vpc_id
  private_subnet_ids = module.vpc.private_subnet_ids
  ingress_sg_id      = module.vpc.service_security_group_id
  instance_class     = var.db_instance_class
  multi_az           = var.environment == "production"
  tags               = local.tags
}

module "elasticache" {
  source = "./modules/elasticache"

  name               = local.name
  vpc_id             = module.vpc.vpc_id
  private_subnet_ids = module.vpc.private_subnet_ids
  ingress_sg_id      = module.vpc.service_security_group_id
  node_type          = var.redis_node_type
  tags               = local.tags
}

module "s3" {
  source = "./modules/s3"

  name = local.name
  tags = local.tags
}

module "ecs" {
  source = "./modules/ecs"

  name               = local.name
  region             = var.region
  vpc_id             = module.vpc.vpc_id
  public_subnet_ids  = module.vpc.public_subnet_ids
  private_subnet_ids = module.vpc.private_subnet_ids
  service_sg_id      = module.vpc.service_security_group_id
  alb_sg_id          = module.vpc.alb_security_group_id
  execution_role_arn = module.iam.execution_role_arn
  api_task_role_arn  = module.iam.api_task_role_arn
  indexer_role_arn   = module.iam.indexer_task_role_arn
  ssm_prefix         = local.ssm_prefix
  api_desired_count  = var.api_desired_count
  tags               = local.tags
}
