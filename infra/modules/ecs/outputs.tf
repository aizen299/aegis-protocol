output "cluster_name" { value = aws_ecs_cluster.this.name }
output "alb_dns_name" { value = aws_lb.api.dns_name }
output "alb_target_group_arn" { value = aws_lb_target_group.api.arn }
output "api_repository_url" { value = aws_ecr_repository.api.repository_url }
output "indexer_repository_url" { value = aws_ecr_repository.indexer.repository_url }
