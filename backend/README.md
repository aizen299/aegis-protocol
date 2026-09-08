# Backend

Go services: the blockchain event indexer and the REST API. This is the intelligence and
coordination layer — contracts settle, the backend interprets.

## Status

| Version | Component | State |
|---|---|---|
| v0.1 | `cmd/indexer`, `cmd/api`, vault read models | Implemented |
| v0.2 | Oracle aggregation service | Not started |
| v0.3 | Governance state cache | Not started |
| v0.4 | zk proof orchestration | Not started |

## Commands

```bash
go build ./...
go test ./... -race -count=1
go test ./internal/indexer/... -run TestStepStopsAtConfirmationDepth -v
migrate -path migrations/ -database $DB_DSN up
```

Regenerate the vault ABI after any change to the contract's external surface:

```bash
make backend-abi
```

## Layering

```
cmd/            process entry points, signal handling, wiring
internal/
  chain/        VM-agnostic chain interface        <- indexer/vault depend on this
    evm/        the only package that may import ethclient
  indexer/      cursor-based, idempotent, reorg-safe event consumption
  db/           pgx/v5, parameterised query constants, no ORM
  cache/        Redis, TTL on every key, cache-aside
  vault/        read models over indexed state
  api/          chi router, JSON handlers
pkg/
  types/        chain-agnostic domain types (Identity is 32 bytes)
  contracts/    generated/exported ABIs — EVM-specific by definition
  config/       env-driven typed config, fail fast
```

## Multi-chain boundary

Three constraints from `docs/project-spec.md` §7 are enforced here, two of them by tests in
`internal/architecture/`:

1. **Identity is 32 bytes.** `types.Identity` is `[32]byte`; an EVM address occupies the low 20.
   `TestPkgTypesHasNoChainDrivers` fails if `pkg/types` grows a chain driver import.
2. **`chain/` is an interface.** `TestOnlyChainEVMImportsGoEthereum` fails if any package other
   than `internal/chain/evm` and `pkg/contracts` imports go-ethereum. The `evm` client normalises
   `common.Address` to `types.Identity` at its boundary, so nothing downstream sees a 20-byte
   address.
3. **State is chain-scoped.** Cursors are keyed `(service_name, chain_id)`; every chain-derived
   row carries `chain_id`; Redis keys are `pb:{module}:{chain_id}:{entity}:{id}`.

## Indexer guarantees

- **Cursor-based.** Progress persists to `indexer_cursors`, keyed per chain. `SaveCursor` is
  monotonic, so a restarted process cannot rewind a cursor another has advanced.
- **Reorg-safe.** Nothing above `head - ConfirmationDepth` is processed. The depth lives behind
  `chain.Client` because finality is not the same concept on every chain.
- **Idempotent.** The cursor commits only after every event in a batch is durable, so a crash
  replays the batch. Handlers absorb that with `ON CONFLICT DO NOTHING` on
  `(chain_id, tx_hash, log_index)`. The `log_index` is load-bearing: one transaction can emit
  several `Deposited` events, and a conflict target of `(chain_id, tx_hash)` alone would silently
  discard all but the first.
- **Restart-safe.** Resumes from the persisted cursor, never from memory or `START_BLOCK`.

## Endpoints

```
GET /health
GET /ready                                       db + cache reachability
GET /v1/vault/{vaultAddress}/tvl
GET /v1/vault/positions/{address}
GET /v1/vault/positions/{address}/deposits?limit=&offset=
```

Addresses are validated through the chain client's own decoder, not a hex regex — the canonical
encoding differs per chain.

The API listens on **8090**, not 8080; 8080 is commonly occupied by Jenkins.

## Amounts and decimals

Token amounts are `types.Raw` end to end — an unscaled `uint256` in the asset's own base units,
never rescaled on ingest, and marshalled as a JSON *string* because a `uint256` does not survive a
`float64`.

Decimals are a property of the asset. The indexer resolves them once per asset through
`chain.Client.TokenMetadata` and writes them to `assets`; a foreign key from every amount-bearing
table makes it impossible to store an amount whose scale is unknown. A token that does not expose
`decimals()` fails the batch rather than defaulting — guessing a scale is a silent
twelve-order-of-magnitude error on a six-decimal asset like USDC. `TestVaultRowIsIndependentOfAssetDecimals`
holds that line.

Every response carrying an amount carries its `decimals` alongside, so scaling happens once, at the
presentation edge.

## End-to-end test

`make e2e` from the repo root. Every other suite here tests a component against a mock of its
neighbour; `internal/e2e` is the only place a real contract emits a real log that a real indexer
writes to a real database and a real handler serves.

It deploys against a **six-decimal** token on purpose. Eighteen is the value every layer would get
right by accident, so a hardcoded scale anywhere in the path shows up as a factor of 10^12 rather
than passing silently.

What it covers beyond the happy path:

| Test | What would otherwise go unnoticed |
|---|---|
| `TestDepositFlowPreservesRawSixDecimalAmount` | a rescale anywhere between the log and the API |
| `TestAssetMetadataIsResolvedFromChain` | `decimals()`/`symbol()` never actually being read |
| `TestAPIServesRawAmountAndDecimals` | a uint256 serialised as a JSON number |
| `TestAPIAcceptsChecksummedAddress` | checksummed input failing to reach the stored lowercase row |
| `TestReindexingIsIdempotent` | the conflict target not catching a replayed batch |
| `TestUnconfirmedBlocksAreNotIndexed` | the reorg window not being respected |
| `TestTVLReflectsIndexedFlows` | withdrawals not netting against deposits |
| `TestOracleRoundSettlesWithGoSignedSubmissions` | the Go and Solidity EIP-712 encodings disagreeing |
| `TestOracleRoundDeactivationDoesNotShrinkQuorumOnChain` | the eligibility snapshot not holding on a real chain |
| `TestOracleReaderRejectsStaleValueOnChain` | a stale price being served |

The oracle signature test is the one that could not be written any other way. Every contract unit
test signs with Foundry's `vm.sign` against a Foundry-deployed address. This one signs with
`internal/chain/evm.SignSubmission` — the code path a node binary will use — against a
script-deployed contract, and lets the contract verify it. A mismatch in the domain separator,
typehash, field order, or the `v` offset surfaces only here.

The tests skip rather than fail when Anvil or Postgres are unreachable, so CI asserts that they
actually ran — a silently skipped smoke test is worse than none.
