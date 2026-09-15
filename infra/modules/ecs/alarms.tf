# CloudWatch alarms required for v1.0 — see docs/devops.md.

resource "aws_cloudwatch_metric_alarm" "api_5xx" {
  alarm_name          = "${var.name}-api-5xx"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  threshold           = 0
  treat_missing_data  = "notBreaching"
  tags                = var.tags

  metric_query {
    id          = "rate"
    expression  = "IF(requests > 0, 100 * errors / requests, 0)"
    label       = "5xx rate (%)"
    return_data = true
  }

  metric_query {
    id = "errors"

    metric {
      namespace   = "AWS/ApplicationELB"
      metric_name = "HTTPCode_Target_5XX_Count"
      period      = 300
      stat        = "Sum"
      dimensions  = { LoadBalancer = aws_lb.api.arn_suffix }
    }
  }

  metric_query {
    id = "requests"

    metric {
      namespace   = "AWS/ApplicationELB"
      metric_name = "RequestCount"
      period      = 300
      stat        = "Sum"
      dimensions  = { LoadBalancer = aws_lb.api.arn_suffix }
    }
  }
}

resource "aws_cloudwatch_metric_alarm" "api_latency_p99" {
  alarm_name          = "${var.name}-api-latency-p99"
  namespace           = "AWS/ApplicationELB"
  metric_name         = "TargetResponseTime"
  extended_statistic  = "p99"
  period              = 300
  evaluation_periods  = 2
  comparison_operator = "GreaterThanThreshold"
  threshold           = 2
  treat_missing_data  = "notBreaching"
  dimensions          = { LoadBalancer = aws_lb.api.arn_suffix }
  tags                = var.tags
}

resource "aws_cloudwatch_metric_alarm" "indexer_stopped" {
  alarm_name          = "${var.name}-indexer-not-running"
  namespace           = "ECS/ContainerInsights"
  metric_name         = "RunningTaskCount"
  statistic           = "Minimum"
  period              = 300
  evaluation_periods  = 2
  comparison_operator = "LessThanThreshold"
  threshold           = 1
  treat_missing_data  = "breaching"
  tags                = var.tags

  dimensions = {
    ClusterName = aws_ecs_cluster.this.name
    ServiceName = aws_ecs_service.indexer.name
  }
}

# ECS publishes no restart counter. A restarting task shows up as the running count dipping below
# what the service wants, so that is what this watches — and it is a proxy, not the metric the
# requirement names. Recorded in docs/v1.0-production-plan.md §2.3 rather than presented as exact.
resource "aws_cloudwatch_metric_alarm" "api_tasks_below_desired" {
  alarm_name          = "${var.name}-api-tasks-below-desired"
  namespace           = "ECS/ContainerInsights"
  metric_name         = "RunningTaskCount"
  statistic           = "Minimum"
  period              = 300
  evaluation_periods  = 2
  comparison_operator = "LessThanThreshold"
  threshold           = var.api_desired_count
  treat_missing_data  = "breaching"
  tags                = var.tags

  dimensions = {
    ClusterName = aws_ecs_cluster.this.name
    ServiceName = aws_ecs_service.api.name
  }
}

# The indexer's lag, in seconds.
#
# docs/devops.md asks for an alarm at 100 blocks. On Arbitrum a block is roughly 0.25s, so that is
# about 25 seconds — inside a single pass of the indexer's own batch window, which would make this
# fire constantly and get muted. Seconds are the stable unit; see docs/v1.0-production-plan.md §2.2.
#
# The threshold is provisional until staging produces a baseline. The plan says to measure it rather
# than guess it, and this default is deliberately loose so a real number replaces a loose alarm
# rather than a tight one that has already trained people to ignore it.
resource "aws_cloudwatch_metric_alarm" "indexer_lag" {
  alarm_name          = "${var.name}-indexer-lag"
  namespace           = "AegisProtocol"
  metric_name         = "IndexerLagSeconds"
  statistic           = "Maximum"
  period              = 300
  evaluation_periods  = 2
  comparison_operator = "GreaterThanThreshold"
  threshold           = var.indexer_lag_alarm_seconds
  treat_missing_data  = "breaching"
  tags                = var.tags

  dimensions = {
    Service     = "indexer"
    Environment = var.environment
  }
}
