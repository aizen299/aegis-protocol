# CloudWatch alarms required for v1.0 — docs/devops.md. They live beside the instance rather than in
# the ecs module so an alarm cannot outlive the thing it watches.

resource "aws_cloudwatch_metric_alarm" "cpu" {
  alarm_name          = "${var.name}-rds-cpu"
  namespace           = "AWS/RDS"
  metric_name         = "CPUUtilization"
  statistic           = "Average"
  period              = 300
  evaluation_periods  = 2
  comparison_operator = "GreaterThanThreshold"
  threshold           = 80
  treat_missing_data  = "notBreaching"
  dimensions          = { DBInstanceIdentifier = aws_db_instance.this.identifier }
  tags                = var.tags
}

# "80% of max" needs a max, and RDS derives max_connections from the instance class through a
# formula in the default parameter group. The number is therefore a variable rather than a literal:
# an alarm quietly computed against the wrong instance class is one that never fires.
resource "aws_cloudwatch_metric_alarm" "connections" {
  alarm_name          = "${var.name}-rds-connections"
  namespace           = "AWS/RDS"
  metric_name         = "DatabaseConnections"
  statistic           = "Maximum"
  period              = 300
  evaluation_periods  = 2
  comparison_operator = "GreaterThanThreshold"
  threshold           = floor(var.max_connections * 0.8)
  treat_missing_data  = "notBreaching"
  dimensions          = { DBInstanceIdentifier = aws_db_instance.this.identifier }
  tags                = var.tags
}
