# An eviction means the cache is full and discarding keys. The protocol treats Redis as a hot-path
# cache with Postgres as the source of truth, so an eviction is not data loss — but it is the point
# at which the cache stops absorbing read load, and the threshold is zero because there is no
# healthy number of them at this size.

resource "aws_cloudwatch_metric_alarm" "evictions" {
  alarm_name          = "${var.name}-redis-evictions"
  namespace           = "AWS/ElastiCache"
  metric_name         = "Evictions"
  statistic           = "Sum"
  period              = 300
  evaluation_periods  = 1
  comparison_operator = "GreaterThanThreshold"
  threshold           = 0
  treat_missing_data  = "notBreaching"
  dimensions          = { ReplicationGroupId = aws_elasticache_replication_group.this.id }
  tags                = var.tags
}
