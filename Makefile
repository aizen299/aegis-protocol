.DEFAULT_GOAL := help
SHELL := /bin/bash

COMPOSE  := docker compose
DB_DSN   ?= postgres://pb:pb_local@localhost:5432/aegis?sslmode=disable
ANVIL_RPC ?= http://127.0.0.1:8545

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# --- top-level ---

.PHONY: dev
dev: ## Bring up the local stack (postgres, redis, anvil, migrate, indexer, api)
	$(COMPOSE) up --build -d
	@echo "api      http://localhost:8090/health"
	@echo "anvil    $(ANVIL_RPC)"

.PHONY: down
down: ## Tear down the local stack, keeping volumes
	$(COMPOSE) down

.PHONY: clean
clean: ## Tear down the local stack and delete volumes
	$(COMPOSE) down -v

.PHONY: logs
logs: ## Follow service logs
	$(COMPOSE) logs -f indexer api

.PHONY: test
test: contracts-test backend-test zk-test ## Run every layer's test suite

.PHONY: build
build: contracts-build backend-build zk-build frontend-build ## Build every layer

.PHONY: lint
lint: contracts-lint backend-lint zk-lint frontend-lint ## Lint every layer

.PHONY: security
security: contracts-slither ## Run static analysis

# --- contracts ---

.PHONY: contracts-build
contracts-build:
	cd contracts && forge build --sizes

.PHONY: contracts-test
contracts-test:
	cd contracts && forge test -vvv

.PHONY: contracts-coverage
contracts-coverage:
	cd contracts && forge coverage --report lcov

.PHONY: contracts-lint
contracts-lint:
	cd contracts && forge fmt --check

.PHONY: contracts-fmt
contracts-fmt:
	cd contracts && forge fmt

.PHONY: contracts-slither
contracts-slither: ## Mandatory before any contract is considered complete
	cd contracts && slither . --config-file slither.config.json

.PHONY: contracts-layout
contracts-layout: ## Dump storage layout — required before any UUPS upgrade
	cd contracts && forge inspect VaultEngine storage-layout

.PHONY: deploy-local
deploy-local: ## Deploy VaultEngine to local anvil
	cd contracts && forge script script/DeployVault.s.sol:DeployVault \
		--rpc-url $(ANVIL_RPC) --broadcast

# --- backend ---

.PHONY: backend-build
backend-build:
	cd backend && go build ./...

.PHONY: backend-test
backend-test:
	cd backend && go test ./... -race -count=1

.PHONY: backend-lint
backend-lint:
	cd backend && gofmt -l . && go vet ./...

.PHONY: backend-abi
backend-abi: ## Re-export contract ABIs consumed by the indexer
	cd contracts && forge inspect VaultEngine abi --json \
		> ../backend/pkg/contracts/vaultengine/VaultEngine.abi.json

.PHONY: migrate-up
migrate-up:
	migrate -path backend/migrations -database "$(DB_DSN)" up

.PHONY: migrate-down
migrate-down:
	migrate -path backend/migrations -database "$(DB_DSN)" down 1

# --- zk ---

.PHONY: zk-build
zk-build:
	cd zk && cargo build --release

.PHONY: zk-test
zk-test:
	cd zk && cargo test

.PHONY: zk-lint
zk-lint:
	cd zk && cargo fmt --check && cargo clippy --all-targets -- -D warnings

# --- frontend ---

.PHONY: frontend-build
frontend-build:
	cd frontend && npm run build

.PHONY: frontend-lint
frontend-lint:
	cd frontend && npm run typecheck && npm run lint

.PHONY: frontend-dev
frontend-dev:
	cd frontend && npm run dev

# --- infra ---

.PHONY: deploy-staging
deploy-staging: ## Plan and apply the staging environment
	cd infra/environments/staging && terraform init && terraform plan -out=tfplan
	@echo "Review the plan above, then run: cd infra/environments/staging && terraform apply tfplan"
