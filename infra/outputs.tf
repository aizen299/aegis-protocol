output "vpc_id" {
  value = module.vpc.vpc_id
}

output "api_endpoint" {
  description = "Public DNS name of the API load balancer."
  value       = module.ecs.alb_dns_name
}

output "db_endpoint" {
  description = "RDS endpoint. Reachable only from within the VPC."
  value       = module.rds.endpoint
  sensitive   = true
}

output "redis_endpoint" {
  value     = module.elasticache.primary_endpoint
  sensitive = true
}

output "artifacts_bucket" {
  value = module.s3.artifacts_bucket
}
