resource "aws_ecs_cluster" "this" {
  name = var.name
  tags = var.tags

  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

resource "aws_ecr_repository" "api" {
  name                 = "${var.name}-api"
  image_tag_mutability = "IMMUTABLE"
  tags                 = var.tags

  image_scanning_configuration {
    scan_on_push = true
  }
}

resource "aws_ecr_repository" "indexer" {
  name                 = "${var.name}-indexer"
  image_tag_mutability = "IMMUTABLE"
  tags                 = var.tags

  image_scanning_configuration {
    scan_on_push = true
  }
}

resource "aws_cloudwatch_log_group" "api" {
  name              = "/${var.name}/api"
  retention_in_days = var.log_retention_days
  tags              = var.tags
}

resource "aws_cloudwatch_log_group" "indexer" {
  name              = "/${var.name}/indexer"
  retention_in_days = var.log_retention_days
  tags              = var.tags
}

resource "aws_lb" "api" {
  name               = "${var.name}-api"
  load_balancer_type = "application"
  subnets            = var.public_subnet_ids
  security_groups    = [var.alb_sg_id]
  tags               = var.tags

  drop_invalid_header_fields = true
}

resource "aws_lb_target_group" "api" {
  name        = "${var.name}-api"
  port        = 8090
  protocol    = "HTTP"
  vpc_id      = var.vpc_id
  target_type = "ip"
  tags        = var.tags

  health_check {
    path                = "/health"
    matcher             = "200"
    interval            = 30
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }

  deregistration_delay = 30
}

resource "aws_ecs_task_definition" "api" {
  family                   = "${var.name}-api"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = var.api_cpu
  memory                   = var.api_memory
  execution_role_arn       = var.execution_role_arn
  task_role_arn            = var.api_task_role_arn
  tags                     = var.tags

  container_definitions = jsonencode([{
    name      = "api"
    image     = "${aws_ecr_repository.api.repository_url}:${var.image_tag}"
    essential = true

    portMappings = [{ containerPort = 8090, protocol = "tcp" }]

    environment = [
      { name = "SERVICE_NAME", value = "api" },
      { name = "API_ADDR", value = ":8090" },
    ]

    # Secrets are injected from SSM at task start. Never baked into the image or a tfvars file.
    secrets = [
      { name = "DB_DSN", valueFrom = "${var.ssm_prefix}/db_dsn" },
      { name = "REDIS_ADDR", valueFrom = "${var.ssm_prefix}/redis_addr" },
      { name = "CHAIN_RPC_URL", valueFrom = "${var.ssm_prefix}/chain_rpc_url" },
      { name = "CHAIN_ID", valueFrom = "${var.ssm_prefix}/chain_id" },
    ]

    logConfiguration = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = aws_cloudwatch_log_group.api.name
        "awslogs-region"        = var.region
        "awslogs-stream-prefix" = "ecs"
      }
    }

    healthCheck = {
      command     = ["CMD-SHELL", "wget -qO- http://localhost:8090/health || exit 1"]
      interval    = 30
      timeout     = 5
      retries     = 3
      startPeriod = 15
    }
  }])
}

# The indexer is a singleton: two processes on one chain would contend for the same cursor row.
resource "aws_ecs_task_definition" "indexer" {
  family                   = "${var.name}-indexer"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = var.indexer_cpu
  memory                   = var.indexer_memory
  execution_role_arn       = var.execution_role_arn
  task_role_arn            = var.indexer_role_arn
  tags                     = var.tags

  container_definitions = jsonencode([{
    name      = "indexer"
    image     = "${aws_ecr_repository.indexer.repository_url}:${var.image_tag}"
    essential = true

    environment = [{ name = "SERVICE_NAME", value = "indexer" }]

    secrets = [
      { name = "DB_DSN", valueFrom = "${var.ssm_prefix}/db_dsn" },
      { name = "REDIS_ADDR", valueFrom = "${var.ssm_prefix}/redis_addr" },
      { name = "CHAIN_RPC_URL", valueFrom = "${var.ssm_prefix}/chain_rpc_url" },
      { name = "CHAIN_ID", valueFrom = "${var.ssm_prefix}/chain_id" },
      { name = "CONTRACT_VAULT_ENGINE", valueFrom = "${var.ssm_prefix}/contract_vault_engine" },
    ]

    logConfiguration = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = aws_cloudwatch_log_group.indexer.name
        "awslogs-region"        = var.region
        "awslogs-stream-prefix" = "ecs"
      }
    }
  }])
}

resource "aws_ecs_service" "api" {
  name            = "${var.name}-api"
  cluster         = aws_ecs_cluster.this.id
  task_definition = aws_ecs_task_definition.api.arn
  desired_count   = var.api_desired_count
  launch_type     = "FARGATE"
  tags            = var.tags

  network_configuration {
    subnets         = var.private_subnet_ids
    security_groups = [var.service_sg_id]
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.api.arn
    container_name   = "api"
    container_port   = 8090
  }

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  depends_on = [aws_lb_target_group.api]
}

resource "aws_ecs_service" "indexer" {
  name            = "${var.name}-indexer"
  cluster         = aws_ecs_cluster.this.id
  task_definition = aws_ecs_task_definition.indexer.arn
  desired_count   = 1
  launch_type     = "FARGATE"
  tags            = var.tags

  # A second indexer would double-process the same range. Old task stops before the new one starts.
  deployment_minimum_healthy_percent = 0
  deployment_maximum_percent         = 100

  network_configuration {
    subnets         = var.private_subnet_ids
    security_groups = [var.service_sg_id]
  }

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }
}
