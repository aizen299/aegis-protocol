# Backend

Go services: the blockchain event indexer and the REST API. This is the intelligence and
coordination layer — contracts settle, the backend interprets.

## Status

| Version | Component | State |
|---|---|---|
| v0.1 | `cmd/indexer`, `cmd/api`, vault read models | Implemented |
| v0.2 | Oracle indexing (feeds, rounds, submissions, node registry, slashing) | Implemented |
| v0.2 | Oracle aggregation service (`cmd/aggregator`) | Implemented |
| v0.2 | Oracle node binary (`cmd/oraclenode`) | Implemented |
| v0.3 | Governance state cache | Not started |
| v0.4 | zk proof orchestration | Not started |

## Commands

```bash
go build ./...
go test ./... -race -count=1
go test ./internal/indexer/... -run TestStepStopsAtConfirmationDepth -v
migrate -path migrations/ -database $DB_DSN up
```

Regenerate the contract ABIs after any change to an external surface:

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

GET /v1/oracle/feeds?limit=&offset=
GET /v1/oracle/feeds/{feedId}
GET /v1/oracle/feeds/{feedId}/rounds?limit=&offset=
GET /v1/oracle/rounds/{roundId}
GET /v1/oracle/rounds/{roundId}/submissions?limit=&offset=
GET /v1/oracle/nodes?limit=&offset=
GET /v1/oracle/nodes/{address}
```

Every response carrying a value carries its scale beside it — a feed's declared decimals for round
and submission values, the stake asset's for node balances. Values are raw strings, never JSON
numbers: a uint256 does not survive a float64.

Rounds expose the eligibility snapshot the contract froze at open (`eligibleCount`,
`nodeSetVersion`) rather than the live node set, so a settled round can be audited against the set
that actually applied to it.

Rounds and submissions are deliberately uncached. They are the audit trail an operator reaches for
when a settlement looks wrong, and a stale answer there is worse than a slow one. Feeds and the
node registry are cached — they change rarely and are read constantly.

Addresses are validated through the chain client's own decoder, not a hex regex — the canonical
encoding differs per chain.

The API listens on **8090**, not 8080; 8080 is commonly occupied by Jenkins.

## The aggregation service

`cmd/aggregator` analyses settled rounds and submits the penalties it decides. It is the only
process in the backend that signs a state-changing transaction.

Deciding and executing are separate types with separate privileges. The `Aggregator` verifies
signatures, medianizes independently, and records decisions — it holds no key and cannot move
anything. The `Executor` holds the key and makes no judgements. A crash between them loses nothing,
because the decision is durable in Postgres before the executor runs.

An independent median that disagrees with the settled value is an **alert, never a correction**. The
chain is authoritative; a backend that overrode a settled price would be claiming the authority
staking and slashing exist to deny it. Both numbers are kept so the disagreement can be diagnosed.

`submitted_at` and `executed_at` are distinct. The executor sets the first; the indexer sets the
second when it sees `NodeSlashed`. A sent transaction is not a landed one, and a decision stuck
between the two is an operational alert rather than a resting state.

Retrying is safe because the contract permits one penalty per node per round. The executor cannot
know whether a transaction it lost track of landed, so the guard has to be on chain — a retry
reverts instead of taking the stake twice.

## The oracle node

`cmd/oraclenode` fetches a price from several independent sources, takes the median across them,
signs it, and submits it to the open round.

**Two medians, protecting different things.** The contract's median over node submissions protects
the feed from a bad node. This one, over sources, protects the node from a bad source — a single
compromised endpoint cannot move what this node reports, and reporting something nobody else does
is what gets a node slashed.

**Prices never touch a float.** `3000.42` has no exact binary representation, and the error would
compound through medianization into a value that disagrees with what every other node computed from
the same input. Parsing is exact decimal-to-integer at the feed's scale, with excess precision
truncated toward zero.

**Silence beats a guess.** Below `NODE_MIN_SOURCES` answering, the node skips the round. A missed
round costs 0.5% of stake; a submission derived from one source costs 1% and misleads every
consumer of the feed. That floor is checked against the configured source count at startup, so a
node that could never submit refuses to start.

It holds its own key under the same policy as the slasher key — a node key can stake, unstake, and
produce submissions attributed to that node, so it is not a lesser secret.

## Secrets

`APP_ENV` is required and has no default. It selects where sensitive material comes from, and it is
declared rather than inferred — a process must never decide it is in development because a
development variable happens to be set.

| `APP_ENV` | Source for the SLASHER_ROLE key |
|---|---|
| `local` | the environment variable named by `SLASHER_KEY_REF` |
| `staging`, `production` | AWS SSM Parameter Store, at the path in `SLASHER_KEY_REF` |

There is no fallback in either direction. If the required source is unregistered, unreachable,
empty, or returns malformed material, startup fails. A service that degrades to a weaker source is
worse than one that will not start, because the degradation is silent and the weaker source is
usually a development key.

Outside `local` the environment provider is not even registered, so two independent things would
have to be wrong for a staging process to read a key out of its environment.
`TestStagingRefusesEvenWithTheDevelopmentVariablePresent` holds that line.

The provider name is logged at startup so an operator can confirm the source from logs alone.
Material never is: `secrets.Secret` redacts through `String`, `GoString`, `Format`, and
`MarshalJSON`, so `%v`, `%s`, `%q`, `%#v`, zerolog reflection, and `encoding/json` all print
`[redacted]`. Reading the value requires `Expose()`, which is greppable — an audit can enumerate
every place the real value is touched.

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
