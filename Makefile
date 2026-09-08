.DEFAULT_GOAL := help
SHELL := /bin/bash

COMPOSE  := docker compose
LAYOUT_BASELINE ?= v0.1.0
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

.PHONY: e2e
e2e: ## End-to-end smoke test: real chain, real database, real indexer and API
	./scripts/e2e.sh

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
contracts-layout: ## Dump the current storage layout
	cd contracts && forge inspect VaultEngine storage-layout

.PHONY: contracts-layout-check
contracts-layout-check: ## Fail if storage layout diverges from the released baseline (run before any UUPS upgrade)
	@cd contracts && forge inspect VaultEngine storage-layout --json \
		| jq -S '[.storage[] | {label, slot, offset, type: (.type | gsub("[0-9]+$$"; ""))}]' > /tmp/layout-current.json
	@jq -S '[.storage[] | {label, slot, offset, type: (.type | gsub("[0-9]+$$"; ""))}]' \
		contracts/deployments/layouts/VaultEngine.$(LAYOUT_BASELINE).json > /tmp/layout-baseline.json
	@diff -u /tmp/layout-baseline.json /tmp/layout-current.json \
		&& echo "storage layout matches $(LAYOUT_BASELINE)" \
		|| (echo "STORAGE LAYOUT DIVERGED from $(LAYOUT_BASELINE). Append-only: never remove or reorder a variable." && exit 1)

.PHONY: contracts-layout-record
contracts-layout-record: ## Record the current layout as a new release baseline (RELEASE=v0.2.0)
	@test -n "$(RELEASE)" || (echo "set RELEASE, e.g. make contracts-layout-record RELEASE=v0.2.0" && exit 1)
	cd contracts && forge inspect VaultEngine storage-layout --json \
		| jq -S '{contract: "VaultEngine", release: "$(RELEASE)", storage: [.storage[] | {label, slot, offset, type}], types: .types}' \
		> deployments/layouts/VaultEngine.$(RELEASE).json
	@echo "recorded contracts/deployments/layouts/VaultEngine.$(RELEASE).json"

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

.PHONY: backend-e2e
backend-e2e: ## Assumes anvil, postgres, and redis are already up — prefer `make e2e`
	cd backend && go test -tags e2e ./internal/e2e/... -count=1 -v

.PHONY: backend-lint
backend-lint:
	cd backend && gofmt -l . && go vet ./...

.PHONY: backend-abi
backend-abi: ## Re-export contract ABIs consumed by the indexer
	cd contracts && forge inspect VaultEngine abi --json \
		> ../backend/pkg/contracts/vaultengine/VaultEngine.abi.json
	cd contracts && forge inspect OracleRounds abi --json \
		> ../backend/pkg/contracts/oracle/OracleRounds.abi.json
	cd contracts && forge inspect OracleStaking abi --json \
		> ../backend/pkg/contracts/oracle/OracleStaking.abi.json

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
