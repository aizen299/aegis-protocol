.DEFAULT_GOAL := help
SHELL := /bin/bash

COMPOSE  := docker compose
LAYOUT_BASELINE ?= v0.1.0

# Anvil's first default account. Public knowledge and worthless off a local chain, so the local
# deploy targets work with no setup. Override for anything that is not Anvil.
ANVIL_DEPLOYER_KEY ?= 0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80
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

.PHONY: aggregator
aggregator: ## Run the oracle aggregation service (holds the SLASHER_ROLE key)
	$(COMPOSE) --profile oracle up --build -d aggregator

.PHONY: oraclenode
oraclenode: ## Run one oracle node (holds its own signing key)
	$(COMPOSE) --profile oracle up --build -d oraclenode

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
lint: contracts-lint backend-lint zk-lint frontend-lint docs-check ## Lint every layer

.PHONY: docs-check
docs-check: ## Verify the README's claims against the repository
	./scripts/docs-check.sh

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

# Every upgradeable contract with a released baseline. A contract absent from this list is not
# checked, so adding one here is part of releasing it.
#
# Types are compared with AST node ids stripped from struct, contract, and enum references: those
# ids shift when unrelated files enter the compilation unit. Array lengths are deliberately NOT
# stripped — a gap that changes size is a real layout change.
LAYOUT_CONTRACTS ?= VaultEngine OracleStaking OracleRounds Governor Timelock

.PHONY: contracts-layout-check
contracts-layout-check: ## Fail if any storage layout diverges from its released baseline (run before any UUPS upgrade)
	@set -e; for contract in $(LAYOUT_CONTRACTS); do \
		baseline=$$(ls contracts/deployments/layouts/$$contract.*.json 2>/dev/null | sort | tail -1); \
		if [ -z "$$baseline" ]; then echo "$$contract: no baseline recorded yet, skipping"; continue; fi; \
		(cd contracts && forge inspect $$contract storage-layout --json \
			| jq -S '[.storage[] | {label, slot, offset, type: (.type | gsub("(?<a>t_(struct|contract|enum)\\([^)]*\\))[0-9]+"; .a))}]') > /tmp/layout-current.json; \
		jq -S '[.storage[] | {label, slot, offset, type: (.type | gsub("(?<a>t_(struct|contract|enum)\\([^)]*\\))[0-9]+"; .a))}]' "$$baseline" > /tmp/layout-baseline.json; \
		if [ "$$(jq 'length' /tmp/layout-current.json)" = "0" ] || [ "$$(jq 'length' /tmp/layout-baseline.json)" = "0" ]; then \
			echo "$$contract: empty storage layout — an unreadable layout must not read as a match. Run 'forge clean'."; \
			exit 1; \
		fi; \
		if diff -u /tmp/layout-baseline.json /tmp/layout-current.json > /tmp/layout-diff.txt; then \
			echo "$$contract matches $$(basename $$baseline)"; \
		else \
			cat /tmp/layout-diff.txt; \
			echo "STORAGE LAYOUT DIVERGED: $$contract vs $$(basename $$baseline). Append-only: never remove or reorder a variable."; \
			exit 1; \
		fi; \
	done

.PHONY: contracts-layout-record
contracts-layout-record: ## Record current layouts as a release baseline (RELEASE=v0.2.0)
	@test -n "$(RELEASE)" || (echo "set RELEASE, e.g. make contracts-layout-record RELEASE=v0.2.0" && exit 1)
	@set -e; for contract in $(LAYOUT_CONTRACTS); do \
		(cd contracts && forge inspect $$contract storage-layout --json \
			| jq -S --arg c "$$contract" --arg r "$(RELEASE)" \
			  '{contract: $$c, release: $$r, storage: [.storage[] | {label, slot, offset, type}], types: .types}') \
			> /tmp/layout-record.json; \
		if [ "$$(jq '.storage | length' /tmp/layout-record.json)" = "0" ]; then \
			echo "$$contract: forge returned an empty layout. Run 'forge clean' and retry."; \
			exit 1; \
		fi; \
		cp /tmp/layout-record.json contracts/deployments/layouts/$$contract.$(RELEASE).json; \
		echo "recorded contracts/deployments/layouts/$$contract.$(RELEASE).json"; \
	done

.PHONY: deploy-local
deploy-local: ## Deploy the v0.1 vault and a six-decimal test token to local anvil
	cd contracts && PRIVATE_KEY=$(ANVIL_DEPLOYER_KEY) forge script script/DeployLocal.s.sol:DeployLocal \
		--rpc-url $(ANVIL_RPC) --broadcast

.PHONY: deploy-oracle-local
deploy-oracle-local: ## Deploy the v0.2 oracle contracts to local anvil
	cd contracts && PRIVATE_KEY=$(ANVIL_DEPLOYER_KEY) forge script script/DeployOracleLocal.s.sol:DeployOracleLocal \
		--rpc-url $(ANVIL_RPC) --broadcast

.PHONY: deploy-vault
deploy-vault: ## Deploy VaultEngine against an existing asset (needs PRIVATE_KEY, VAULT_ASSET, VAULT_ADMIN)
	@test -n "$(PRIVATE_KEY)" || (echo "set PRIVATE_KEY; this target is not local-only and has no default" && exit 1)
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
	cd contracts && forge inspect Governor abi --json \
		> ../backend/pkg/contracts/governance/Governor.abi.json

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
