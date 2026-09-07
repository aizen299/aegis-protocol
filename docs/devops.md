# DevOps & Infrastructure Domain Reference — Project Blockchain

## Local Development Stack

```yaml
# docker-compose.yml (root of monorepo)
version: "3.9"
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: project_blockchain
      POSTGRES_USER: pb
      POSTGRES_PASSWORD: pb_local
    ports: ["5432:5432"]
    volumes: ["pgdata:/var/lib/postgresql/data"]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U pb"]
      interval: 5s
      timeout: 5s
      retries: 5

  redis:
    image: redis:7-alpine
    ports: ["6379:6379"]
    command: redis-server --appendonly yes
    volumes: ["redisdata:/data"]

  indexer:
    build:
      context: ./backend
      dockerfile: docker/indexer.Dockerfile
    environment:
      DB_DSN: postgres://pb:pb_local@postgres:5432/project_blockchain
      REDIS_ADDR: redis:6379
      CHAIN_RPC_URL: ${CHAIN_RPC_URL}
      CHAIN_WS_URL: ${CHAIN_WS_URL}
    depends_on:
      postgres: { condition: service_healthy }
      redis: { condition: service_started }

  api:
    build:
      context: ./backend
      dockerfile: docker/api.Dockerfile
    ports: ["8080:8080"]
    environment:
      DB_DSN: postgres://pb:pb_local@postgres:5432/project_blockchain
      REDIS_ADDR: redis:6379
    depends_on:
      postgres: { condition: service_healthy }

  zk-service:
    build:
      context: ./zk
      dockerfile: Dockerfile
    ports: ["8081:8081"]

volumes:
  pgdata:
  redisdata:
```

## Dockerfile Standards

Multi-stage builds. Final image must be minimal.

```dockerfile
# backend/docker/api.Dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /api ./cmd/api

FROM gcr.io/distroless/static-debian12
COPY --from=builder /api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
```

```dockerfile
# zk/Dockerfile
FROM rust:1.78-slim AS builder
WORKDIR /app
COPY Cargo.toml Cargo.lock ./
COPY src ./src
COPY circuits ./circuits
RUN cargo build --release

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/target/release/zk-service /zk-service
COPY --from=builder /app/circuits /circuits
EXPOSE 8081
ENTRYPOINT ["/zk-service"]
```

## AWS Architecture

```
VPC (10.0.0.0/16)
├── Public Subnets (2 AZs)
│   └── Application Load Balancer
├── Private Subnets (2 AZs)
│   ├── ECS Cluster
│   │   ├── indexer service (1 task, no LB)
│   │   ├── api service (2+ tasks behind ALB)
│   │   ├── oracle-aggregator service (1 task)
│   │   └── zk-service (1 task, internal only)
│   ├── RDS PostgreSQL (Multi-AZ)
│   └── ElastiCache Redis (cluster mode disabled, 1 replica)
└── S3 (circuit artifacts, logs, Terraform state)
```

## Terraform Structure

```
infra/
├── main.tf
├── variables.tf
├── outputs.tf
├── modules/
│   ├── vpc/
│   ├── ecs/
│   ├── rds/
│   ├── elasticache/
│   ├── s3/
│   └── iam/
└── environments/
    ├── staging/
    │   ├── main.tf
    │   └── terraform.tfvars
    └── production/
```

State stored in S3 with DynamoDB locking:
```hcl
terraform {
  backend "s3" {
    bucket         = "pb-terraform-state"
    key            = "staging/terraform.tfstate"
    region         = "us-east-1"
    dynamodb_table = "pb-terraform-locks"
    encrypt        = true
  }
}
```

## ECS Task Definition Pattern

```json
{
  "family": "pb-api",
  "networkMode": "awsvpc",
  "requiresCompatibilities": ["FARGATE"],
  "cpu": "512",
  "memory": "1024",
  "executionRoleArn": "arn:aws:iam::ACCOUNT:role/pb-ecs-execution-role",
  "taskRoleArn": "arn:aws:iam::ACCOUNT:role/pb-api-task-role",
  "containerDefinitions": [
    {
      "name": "api",
      "image": "ACCOUNT.dkr.ecr.us-east-1.amazonaws.com/pb-api:latest",
      "essential": true,
      "portMappings": [{"containerPort": 8080, "protocol": "tcp"}],
      "secrets": [
        {"name": "DB_DSN", "valueFrom": "arn:aws:ssm:us-east-1:ACCOUNT:parameter/pb/staging/db_dsn"},
        {"name": "REDIS_ADDR", "valueFrom": "arn:aws:ssm:us-east-1:ACCOUNT:parameter/pb/staging/redis_addr"}
      ],
      "logConfiguration": {
        "logDriver": "awslogs",
        "options": {
          "awslogs-group": "/pb/api",
          "awslogs-region": "us-east-1",
          "awslogs-stream-prefix": "ecs"
        }
      },
      "healthCheck": {
        "command": ["CMD-SHELL", "wget -qO- http://localhost:8080/health || exit 1"],
        "interval": 30,
        "timeout": 5,
        "retries": 3
      }
    }
  ]
}
```

## IAM Principles

- Each ECS task has its own IAM role with minimum required permissions.
- No shared task roles between services.
- No `*` resources in policies.
- RDS access via security groups, not IAM auth (simpler at this scale).
- S3 access scoped to specific bucket prefixes per service.

## GitHub Actions CI/CD

```yaml
# .github/workflows/contracts.yml
name: Contracts CI
on:
  push:
    paths: ["contracts/**"]
  pull_request:
    paths: ["contracts/**"]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { submodules: recursive }
      - name: Install Foundry
        uses: foundry-rs/foundry-toolchain@v1
      - name: Run tests
        working-directory: contracts
        run: |
          forge build --sizes
          forge test -vvv
          forge coverage --report lcov
      - name: Run Slither
        uses: crytic/slither-action@v0.3.0
        with:
          target: contracts/src/
```

```yaml
# .github/workflows/backend.yml
name: Backend CI
on:
  push:
    paths: ["backend/**"]

jobs:
  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16-alpine
        env: { POSTGRES_DB: pb_test, POSTGRES_USER: pb, POSTGRES_PASSWORD: pb }
        options: >-
          --health-cmd pg_isready
          --health-interval 5s
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.22" }
      - name: Test
        working-directory: backend
        run: go test ./... -race -count=1
      - name: Build
        working-directory: backend
        run: go build ./...
```

## CloudWatch Alarms (Required for v1.0)

| Metric | Threshold | Action |
|---|---|---|
| API 5xx error rate | > 1% over 5 min | Alert |
| API p99 latency | > 2000ms | Alert |
| ECS task restarts | > 2 in 10 min | Alert |
| RDS CPU | > 80% | Alert |
| RDS connection count | > 80% of max | Alert |
| Redis evictions | > 0 | Alert |
| Indexer lag (blocks behind) | > 100 blocks | Alert |

## Secrets Management

Never commit secrets. Never pass via environment files.

Local dev: `.env` (gitignored) with dummy values.
Staging/Prod: AWS SSM Parameter Store (SecureString) or Secrets Manager.

```bash
# Store secret
aws ssm put-parameter \
  --name "/pb/staging/db_dsn" \
  --value "postgres://..." \
  --type SecureString \
  --key-id alias/pb-ssm-key
```

## Monorepo Layout

```
project-blockchain/
├── contracts/          # Solidity + Foundry
├── backend/            # Go services
├── zk/                 # Rust + circuits
├── frontend/           # Next.js
├── infra/              # Terraform
├── .github/
│   └── workflows/
├── docker-compose.yml
└── Makefile
```

`Makefile` targets: `make dev`, `make test`, `make build`, `make deploy-staging`.
