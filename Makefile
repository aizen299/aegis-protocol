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
test: contracts-test backend-test zk-test solana-test frontend-test ## Run every layer's test suite

.PHONY: e2e
e2e: ## End-to-end smoke test: real chain, real database, real indexer and API
	./scripts/e2e.sh

.PHONY: secrets-localstack
secrets-localstack: ## Exercise the real SSM secret path against LocalStack (needs docker)
	./scripts/secrets-localstack.sh

.PHONY: build
build: contracts-build backend-build zk-build solana-build frontend-build ## Build every layer

.PHONY: lint
lint: contracts-lint backend-lint zk-lint solana-lint frontend-lint docs-check ## Lint every layer

.PHONY: docs-check
docs-check: ## Verify the README's claims against the repository
	./scripts/docs-check.sh

.PHONY: security
security: contracts-slither ## Run static analysis

# --- contracts ---

.PHONY: contracts-build
contracts-build:
	cd contracts && forge build --sizes

# BrowserProofTest verifies an artifact that `make zk-browser-proof-check` generates, so it cannot
# run from a clean checkout. Excluded here and run there, rather than made to skip when the artifact
# is missing — a test that passes when its subject is absent is worse than one that does not run.
CONTRACTS_TEST_EXCLUDE ?= BrowserProofTest

.PHONY: contracts-test
contracts-test:
	cd contracts && forge test -vvv --no-match-contract '$(CONTRACTS_TEST_EXCLUDE)'

.PHONY: contracts-test-all
contracts-test-all: ## Every suite, including those needing generated artifacts
	cd contracts && forge test -vvv

.PHONY: contracts-lint
contracts-lint:
	cd contracts && forge fmt --check

.PHONY: contracts-fmt
contracts-fmt:
	cd contracts && forge fmt

# Poseidon is generated from circomlib, never hand-written. Node is required only here: the output
# is committed Solidity, so `forge build` and `forge test` never need it.
POSEIDON_OUT = contracts/src/zk/poseidon

.PHONY: poseidon-gen
poseidon-gen: ## Regenerate the Poseidon contract artifact from the pinned circomlibjs (needs Node)
	cd tools/poseidon && npm ci --no-audit --no-fund && node generate.js

.PHONY: poseidon-check
poseidon-check: ## Fail if the committed Poseidon artifact differs from a fresh generation (needs Node)
	@set -e; \
	tmp=$$(mktemp -d); \
	( cd tools/poseidon && npm ci --silent --no-audit --no-fund >/dev/null && node generate.js "$$tmp" >/dev/null ); \
	fresh="$$tmp/PoseidonBytecode.sol"; \
	if [ ! -s "$$fresh" ]; then \
		echo "poseidon: generation produced an empty artifact — an unreadable output must not read as a match."; \
		rm -rf "$$tmp"; exit 1; \
	fi; \
	committed=$$(shasum -a 256 < "$(POSEIDON_OUT)/PoseidonBytecode.sol" | cut -d" " -f1); \
	generated=$$(shasum -a 256 < "$$fresh" | cut -d" " -f1); \
	if [ "$$committed" = "$$generated" ]; then \
		echo "Poseidon artifact matches a fresh generation (sha256 $$committed)"; \
		rm -rf "$$tmp"; \
	else \
		echo "GENERATED POSEIDON HAS DRIFTED."; \
		echo "  committed: $$committed"; \
		echo "  generated: $$generated"; \
		echo "  differing lines: $$(diff "$(POSEIDON_OUT)/PoseidonBytecode.sol" "$$fresh" | grep -c "^[<>]")"; \
		echo "The bytecode lines are ~20KB each, so the content is not printed. Run 'make poseidon-gen' and commit the result."; \
		rm -rf "$$tmp"; exit 1; \
	fi

.PHONY: zk-circuits-test
zk-circuits-test: ## Run the Noir circuit tests, negative cases included
	cd zk/circuits/vault_membership && nargo test

# The circuit and the contract must agree on tree depth. They cannot import a shared constant
# across languages, and a mismatch is invisible until a proof fails to verify against any root.
.PHONY: zk-depth-check
zk-depth-check: ## Fail if the circuit and the contract disagree on the Merkle tree depth
	@circuit=$$(grep -oE '^global TREE_DEPTH: u32 = [0-9]+' zk/circuits/vault_membership/src/main.nr | grep -oE '[0-9]+$$'); \
	contract=$$(grep -oE 'uint32 public constant TREE_DEPTH = [0-9]+' contracts/src/zk/CommitmentTree.sol | grep -oE '[0-9]+$$'); \
	if [ -z "$$circuit" ] || [ -z "$$contract" ]; then \
		echo "zk-depth-check: could not read the depth from one of the two files — an unread value must not pass."; \
		echo "  circuit: '$$circuit'  contract: '$$contract'"; \
		exit 1; \
	fi; \
	if [ "$$circuit" != "$$contract" ]; then \
		echo "TREE DEPTH MISMATCH: circuit says $$circuit, CommitmentTree says $$contract."; \
		echo "A proof built at one depth can never match a root built at another."; \
		exit 1; \
	fi; \
	echo "tree depth agrees: $$circuit"

# The verifier is generated from the circuit and the pinned toolchain, never hand-written. Node is
# not involved; nargo and bb are, so these are separate from the Poseidon targets.
.PHONY: zk-verifier-gen
zk-verifier-gen: ## Regenerate the Solidity verifier from the circuit (needs nargo and bb)
	./tools/gen-verifier.sh

.PHONY: zk-verifier-check
zk-verifier-check: ## Fail if the committed verifier differs from a fresh generation
	@set -e; \
	tmp=$$(mktemp -d); \
	./tools/gen-verifier.sh "$$tmp" >/dev/null; \
	if [ ! -s "$$tmp/HonkVerifier.sol" ]; then \
		echo "verifier: generation produced nothing — an unreadable output must not read as a match."; \
		rm -rf "$$tmp"; exit 1; \
	fi; \
	committed=$$(shasum -a 256 < contracts/generated/HonkVerifier.sol | cut -d" " -f1); \
	generated=$$(shasum -a 256 < "$$tmp/HonkVerifier.sol" | cut -d" " -f1); \
	rm -rf "$$tmp"; \
	if [ "$$committed" = "$$generated" ]; then \
		echo "verifier matches a fresh generation (sha256 $$committed)"; \
	else \
		echo "GENERATED VERIFIER HAS DRIFTED."; \
		echo "  committed: $$committed"; \
		echo "  generated: $$generated"; \
		echo "The circuit or the toolchain changed. Run 'make zk-verifier-gen', regenerate the"; \
		echo "proof fixture with 'make zk-proof-fixture', and commit both."; \
		exit 1; \
	fi

# The browser proving path must agree with the CLI one: same circuit, same toolchain versions, one
# committed verifier. A version bump on either side without the other must fail here.
.PHONY: zk-browser-proof-check
zk-browser-proof-check: ## Prove through noir_js/bb.js and verify against the committed verifier
	./scripts/browser-proof-check.sh

.PHONY: zk-proof-fixture
zk-proof-fixture: ## Regenerate the committed proof the contract suite verifies (needs nargo, bb, Node)
	./tools/gen-proof-fixture.sh

.PHONY: zk-service-e2e
zk-service-e2e: ## Prove for real through the Rust service (needs nargo and bb)
	# Single-threaded: the leftover-file assertion scans the whole circuit directory, so it cannot
	# run beside another test that is mid-proof. The concurrency test spawns its own threads.
	cd zk && cargo test --test prove_integration -- --ignored --test-threads=1

# Load profiles from docs/v1.0-production-plan.md §2.5. The read profile points at any running API;
# the ingest profile runs under the end-to-end harness because it needs a chain.
.PHONY: load-read
load-read: ## Drive the read path and check it against the alarm thresholds (API_BASE=...)
	cd backend && go run ./cmd/loadgen \
		-base $(or $(API_BASE),http://localhost:8090) \
		-paths $(or $(LOAD_PATHS),/v1/oracle/feeds,/v1/oracle/nodes) \
		-rate $(or $(LOAD_RATE),200) \
		-duration $(or $(LOAD_DURATION),30s)

.PHONY: load-ingest
load-ingest: ## Replay a dense block range and measure how fast the indexer catches up
	cd backend && go test -tags e2e ./internal/e2e/... -count=1 -timeout 15m -v \
		-run TestIngestKeepsUpWithADenseBlockRange

.PHONY: zk-toolchain-check
zk-toolchain-check: ## Fail if the installed Noir toolchain is not the pinned one
	@pinned=$$(awk '/^nargo /{print $$2}' zk/circuits/toolchain.txt); \
	if ! command -v nargo >/dev/null 2>&1; then \
		echo "nargo is not on PATH. Install the pinned version: noirup --version $$pinned"; \
		exit 1; \
	fi; \
	installed=$$(nargo --version | awk -F'= ' '/nargo version/{print $$2}'); \
	if [ "$$installed" != "$$pinned" ]; then \
		echo "nargo $$installed is installed, but zk/circuits/toolchain.txt pins $$pinned."; \
		echo "The generated verifier is a function of this version. Run: noirup --version $$pinned"; \
		exit 1; \
	fi; \
	echo "nargo $$installed matches the pin"; \
	bb_pinned=$$(awk '/^bb /{print $$2}' zk/circuits/toolchain.txt); \
	if ! command -v bb >/dev/null 2>&1; then \
		echo "bb is not on PATH. The verifier cannot be generated without it: run bbup."; \
		exit 1; \
	fi; \
	bb_installed=$$(bb --version | head -1 | tr -d '\r'); \
	if [ "$$bb_installed" != "$$bb_pinned" ]; then \
		echo "bb $$bb_installed is installed, but the pin is $$bb_pinned."; \
		echo "bb and nargo are not independent — see zk/circuits/toolchain.txt. Run: bbup"; \
		exit 1; \
	fi; \
	echo "bb $$bb_installed matches the pin"; \
	dep=$$(awk '/^poseidon /{print $$2}' zk/circuits/toolchain.txt); \
	grep -q "tag = \"$$dep\"" zk/circuits/vault_membership/Nargo.toml || { \
		echo "vault_membership/Nargo.toml does not pin poseidon $$dep"; exit 1; }; \
	echo "poseidon $$dep matches the pin"

# Coverage instruments the bytecode, which changes every contract's creation code and therefore its
# CREATE2 address. The zk suites deploy the gate at an address the committed proof binds as a public
# input, so under instrumentation that address moves and no proof verifies. They are excluded here
# and run in full under `forge test`; excluding them costs coverage numbers, not assertions.
COVERAGE_EXCLUDE ?= ZkVaultGateTest|ZkProbeTest|ZkInvariantTest|BrowserProofTest

.PHONY: contracts-coverage
contracts-coverage: ## Coverage report, minus the suites whose addresses instrumentation moves
	cd contracts && forge coverage --report lcov --no-match-contract '$(COVERAGE_EXCLUDE)'

.PHONY: contracts-slither
contracts-slither: ## Mandatory before any contract is considered complete
	cd contracts && slither . --config-file slither.config.json

.PHONY: contracts-layout
contracts-layout: ## Dump the current storage layout
	cd contracts && forge inspect VaultEngine storage-layout

# Every upgradeable contract with a released baseline. A contract absent from this list is not
# checked, so adding one here is part of releasing it.
#
# The rules live in scripts/check-evm-layouts.py: existing variables keep slot, offset, and type, and
# new ones may only be carved from the trailing __gap. Its own tests run first, since a check that
# has quietly become permissive is worse than none.
LAYOUT_CONTRACTS ?= VaultEngine OracleStaking OracleRounds Governor Timelock CommitmentTree ZkVaultGate WormholeDispatcher

.PHONY: contracts-layout-check
contracts-layout-check: ## Fail if any storage layout diverges from its released baseline (run before any UUPS upgrade)
	@python3 scripts/test_check_evm_layouts.py >/dev/null 2>&1 || { python3 scripts/test_check_evm_layouts.py; exit 1; }
	@set -e; for contract in $(LAYOUT_CONTRACTS); do \
		baseline=$$(ls contracts/deployments/layouts/$$contract.*.json 2>/dev/null | sort | tail -1); \
		if [ -z "$$baseline" ]; then echo "$$contract: no baseline recorded yet, skipping"; continue; fi; \
		(cd contracts && forge inspect $$contract storage-layout --json) > /tmp/layout-current.json; \
		python3 scripts/check-evm-layouts.py "$$baseline" /tmp/layout-current.json \
			|| { echo "  $$contract vs $$(basename $$baseline)"; exit 1; }; \
		echo "$$contract matches $$(basename $$baseline)"; \
	done

.PHONY: contracts-layout-record
contracts-layout-record: ## Record current layouts as a release baseline (RELEASE=v0.2.0)
	@test -n "$(RELEASE)" || (echo "set RELEASE, e.g. make contracts-layout-record RELEASE=v0.2.0" && exit 1)
	@set -e; for contract in $(LAYOUT_CONTRACTS); do \
		(cd contracts && forge inspect $$contract storage-layout --json \
			| jq -S --arg c "$$contract" --arg r "$(RELEASE)" \
			  '{contract: $$c, release: $$r, storage: [.storage[] | {label, slot, offset, type}], types: .types}') \
			> /tmp/layout-record.json; \
		entries=$$(jq -e '.storage | length' /tmp/layout-record.json 2>/dev/null || echo ""); \
		if [ -z "$$entries" ] || [ "$$entries" = "0" ]; then \
			echo "$$contract: forge returned no readable layout (got '$$entries')."; \
			echo "This happens for a newly added contract on a stale cache. Run 'forge clean' and retry."; \
			rm -f contracts/deployments/layouts/$$contract.$(RELEASE).json; \
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

.PHONY: deploy-governance-local
deploy-governance-local: ## Deploy the v0.3 governance contracts to local anvil
	cd contracts && PRIVATE_KEY=$(ANVIL_DEPLOYER_KEY) forge script script/DeployGovernanceLocal.s.sol:DeployGovernanceLocal \
		--rpc-url $(ANVIL_RPC) --broadcast

.PHONY: deploy-zk-local
deploy-zk-local: ## Deploy the v0.4 zk contracts to local anvil
	cd contracts && PRIVATE_KEY=$(ANVIL_DEPLOYER_KEY) forge script script/DeployZkLocal.s.sol:DeployZkLocal \
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
	cd contracts && forge inspect CommitmentTree abi --json \
		> ../backend/pkg/contracts/zk/CommitmentTree.abi.json
	cd contracts && forge inspect ZkVaultGate abi --json \
		> ../backend/pkg/contracts/zk/ZkVaultGate.abi.json

.PHONY: backend-idl
BACKEND_IDLS := aegis_vault:solvault aegis_oracle:soloracle aegis_governance_receiver:solreceiver

backend-idl: ## Copy Solana program IDLs consumed by the indexer
	@for pair in $(BACKEND_IDLS); do \
		cp solana/idl/$${pair%%:*}.json backend/pkg/contracts/$${pair##*:}/$${pair%%:*}.json; \
	done

FRONTEND_IDLS := aegis_vault

.PHONY: frontend-idl
frontend-idl: ## Copy Solana program IDLs the frontend builds transactions from
	@for p in $(FRONTEND_IDLS); do cp solana/idl/$$p.json frontend/src/idl/$$p.json; done

.PHONY: frontend-idl-check
frontend-idl-check: ## Fail if a frontend IDL differs from solana/idl
	@set -e; for p in $(FRONTEND_IDLS); do \
		diff -u solana/idl/$$p.json frontend/src/idl/$$p.json \
			|| { echo "IDL DRIFT: frontend/src/idl/$$p.json. Run make frontend-idl."; exit 1; }; \
		echo "$$p frontend IDL matches solana/idl"; \
	done

.PHONY: backend-idl-check
backend-idl-check: ## Fail if an embedded IDL differs from solana/idl
	@set -e; for pair in $(BACKEND_IDLS); do \
		diff -u solana/idl/$${pair%%:*}.json backend/pkg/contracts/$${pair##*:}/$${pair%%:*}.json \
			|| { echo "IDL DRIFT: backend/pkg/contracts/$${pair##*:} is stale. Run 'make backend-idl'."; exit 1; }; \
		echo "$${pair%%:*} IDL matches solana/idl"; \
	done

.PHONY: migrate-up
migrate-up:
	migrate -path backend/migrations -database "$(DB_DSN)" up

.PHONY: migrate-down
migrate-down:
	migrate -path backend/migrations -database "$(DB_DSN)" down 1

# --- wormhole (local tests only) ---

WORMHOLE_OZ_COMMIT := dc44c9f1a4c3b10af99492eed84f83ed244203f6

.PHONY: wormhole-local-deps
wormhole-local-deps: ## Fetch OpenZeppelin 4.9.6 for the vendored Wormhole core, at a pinned commit
	@set -e; dir=tools/wormhole/lib/openzeppelin-contracts; \
	if [ "$$(git -C $$dir rev-parse HEAD 2>/dev/null)" != "$(WORMHOLE_OZ_COMMIT)" ]; then \
		rm -rf $$dir; \
		git clone --quiet --depth 1 --branch v4.9.6 https://github.com/OpenZeppelin/openzeppelin-contracts.git $$dir; \
	fi; \
	got=$$(git -C $$dir rev-parse HEAD); \
	[ "$$got" = "$(WORMHOLE_OZ_COMMIT)" ] || { echo "OpenZeppelin is at $$got, want $(WORMHOLE_OZ_COMMIT)"; exit 1; }

.PHONY: wormhole-local-build
wormhole-local-build: wormhole-local-deps
	cd tools/wormhole && forge build

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

# --- solana ---

SOLANA_PROGRAMS ?= aegis_vault aegis_oracle aegis_governance_receiver

.PHONY: solana-toolchain-check
solana-toolchain-check: ## Fail if anchor or solana differ from the pins in solana/Anchor.toml
	python3 scripts/check-solana-toolchain.py

.PHONY: solana-build
solana-build: solana-toolchain-check
	cd solana && anchor build
	cd solana && cargo build-sbf --manifest-path programs/aegis_governance_receiver/Cargo.toml \
		--features localnet --sbf-out-dir target/localnet

# The instruction tests load the program built above, so they run after it rather than alongside.
.PHONY: solana-test
solana-test: solana-build
	cd solana && cargo test

.PHONY: solana-external-check
solana-external-check: ## Fail if the committed Wormhole Solana binaries differ from their pinned hashes
	cd solana/external && shasum -a 256 -c SHA256SUMS

.PHONY: solana-lint
solana-lint:
	cd solana && cargo fmt --all --check && cargo clippy --all-targets -- -D warnings

# The IDL is to the indexer what an ABI is: committed, and checked against a fresh build.
.PHONY: solana-idl-check
solana-idl-check: solana-build ## Fail if a committed IDL differs from a fresh build
	@set -e; for p in $(SOLANA_PROGRAMS); do \
		diff -u solana/idl/$$p.json solana/target/idl/$$p.json || \
			{ echo "IDL DRIFT: $$p. Copy solana/target/idl/$$p.json to solana/idl/ if the change is intended."; exit 1; }; \
		echo "$$p IDL matches"; \
	done

.PHONY: solana-layout-check
solana-layout-check: solana-build ## Fail if an account layout changes other than by carving from `reserved`
	@set -e; for p in $(SOLANA_PROGRAMS); do \
		python3 scripts/check-solana-layouts.py solana/target/idl/$$p.json solana/layouts/$$p.json; \
	done

# --- frontend ---

.PHONY: frontend-build
frontend-build:
	cd frontend && npm run build

.PHONY: frontend-lint
frontend-lint: frontend-api-types
	cd frontend && npm run typecheck && npm run lint

.PHONY: frontend-test
frontend-test:
	cd frontend && npm test

# The frontend hand-mirrors backend/pkg/types in TypeScript. Nothing connects the two, so a field
# renamed in Go stays compiling and fails at runtime as an undefined.
.PHONY: frontend-api-types
frontend-api-types: ## Fail if the frontend's API types drift from backend/pkg/types
	python3 scripts/check-api-types.py

.PHONY: frontend-dev
frontend-dev:
	cd frontend && npm run dev

# --- infra ---

.PHONY: deploy-staging
deploy-staging: ## Plan and apply the staging environment
	cd infra/environments/staging && terraform init && terraform plan -out=tfplan
	@echo "Review the plan above, then run: cd infra/environments/staging && terraform apply tfplan"
