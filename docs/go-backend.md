# Go Backend Domain Reference — Project Blockchain

## Service Layout

```
backend/
├── cmd/
│   ├── indexer/        # Blockchain event indexer binary
│   ├── oracle/         # Oracle aggregation service binary
│   └── api/            # REST + WebSocket API binary
├── internal/
│   ├── indexer/        # Event parsing, cursor management
│   ├── oracle/         # Data aggregation, validation, slashing
│   ├── vault/          # Vault state, analytics
│   ├── governance/     # Proposal/vote cache and state
│   ├── zk/             # Proof orchestration client
│   ├── db/             # PostgreSQL access layer
│   ├── cache/          # Redis access layer
│   ├── chain/          # Chain client interface + per-chain implementations (evm/)
│   └── api/            # HTTP handlers, WebSocket hub
├── pkg/
│   ├── types/          # Shared domain types
│   ├── contracts/      # Generated ABI bindings (abigen)
│   └── config/         # Config loading and validation
├── migrations/         # SQL migrations
└── docker/
```

## Configuration

Use environment-variable-driven config with a typed struct. Load at startup, fail fast if invalid.

```go
type Config struct {
    DB struct {
        DSN             string        `env:"DB_DSN,required"`
        MaxOpenConns    int           `env:"DB_MAX_OPEN_CONNS" envDefault:"25"`
        MaxIdleConns    int           `env:"DB_MAX_IDLE_CONNS" envDefault:"5"`
        ConnMaxLifetime time.Duration `env:"DB_CONN_MAX_LIFETIME" envDefault:"5m"`
    }
    Redis struct {
        Addr     string `env:"REDIS_ADDR,required"`
        Password string `env:"REDIS_PASSWORD"`
        DB       int    `env:"REDIS_DB" envDefault:"0"`
    }
    Chain struct {
        RPCURL          string `env:"CHAIN_RPC_URL,required"`
        WSURL           string `env:"CHAIN_WS_URL,required"`
        ChainID         int64  `env:"CHAIN_ID,required"`
        StartBlock      uint64 `env:"CHAIN_START_BLOCK" envDefault:"0"`
        ConfirmBlocks   uint64 `env:"CHAIN_CONFIRM_BLOCKS" envDefault:"12"`
    }
}
```

Never hardcode credentials. Secrets pulled from AWS Secrets Manager at boot.

## Structured Logging

Use `zerolog`. One global logger, context-propagated.

```go
log := zerolog.New(os.Stdout).With().
    Timestamp().
    Str("service", "indexer").
    Logger()

// In handlers/goroutines:
log.Info().
    Str("tx_hash", txHash).
    Uint64("block", blockNum).
    Msg("event processed")

log.Error().Err(err).
    Str("contract", addr).
    Msg("failed to decode event")
```

## Graceful Shutdown

```go
func main() {
    ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer cancel()

    svc := NewService(cfg)
    if err := svc.Start(ctx); err != nil {
        log.Fatal().Err(err).Msg("service failed")
    }

    <-ctx.Done()
    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer shutdownCancel()
    svc.Shutdown(shutdownCtx)
}
```

## Multi-Chain Readiness

Phase 1 is single-chain (Arbitrum), but Solana is the confirmed Phase 2 target — see
`project-spec.md` §7. The contracts get rewritten per chain; the backend does not. These two
constraints are binding from v0.1 and exist so that adding an SVM chain later is an additive
change rather than a migration.

**1. Identity is 32 bytes, not 20.**

An EVM address is 20 bytes; a Solana pubkey is 32. Any identity that crosses a layer boundary —
event payloads, `pkg/types` domain types, Postgres columns, API responses — must be wide enough
for both. Do not use `common.Address` in shared domain types.

```go
// pkg/types — chain-agnostic identity.
type Identity [32]byte

func (i Identity) String() string { /* chain-specific encoding via the chain client */ }

// EVM addresses are left-padded into the low 20 bytes.
func IdentityFromEVM(a common.Address) Identity
func (i Identity) EVMAddress() (common.Address, error)  // errors if high 12 bytes are non-zero
```

`common.Address` remains correct inside `internal/chain/evm/` and in generated `pkg/contracts/`
bindings — those are EVM-specific by definition. The conversion happens at that boundary.

**2. `chain/` is an interface, not an EVM client.**

`internal/chain` defines the interface; `internal/chain/evm` implements it. Indexer, oracle, and
vault packages depend only on the interface, so they never import `ethclient` and never need
changes to support a second VM.

```go
// internal/chain
type Client interface {
    ChainID() int64
    Head(ctx context.Context) (uint64, error)
    // Returns normalized events; decoding is the implementation's concern.
    LogsInRange(ctx context.Context, from, to uint64, filters []Filter) ([]Event, error)
    ConfirmationDepth() uint64
}

type Event struct {
    ChainID     int64
    BlockNumber uint64
    TxHash      [32]byte
    Contract    Identity
    Name        string
    Payload     map[string]any
}
```

Reorg semantics differ per chain and belong behind this interface — an EVM confirmation-depth
window is not how Solana finality works. Callers ask for confirmed events; they do not implement
the confirmation rule themselves.

**3. All chain-derived state is chain-scoped.** See `database.md` "Multi-Chain Readiness" —
cursors are keyed by `(service_name, chain_id)` and every chain-derived row carries `chain_id`.
The indexer must therefore treat its cursor as per-chain, and one indexer process serves one
chain.

## Blockchain Event Indexer

Design requirements:
- **Cursor-based**: persist last processed block to PostgreSQL.
- **Idempotent**: processing the same event twice must not corrupt state. Use `ON CONFLICT DO NOTHING` or upsert.
- **Restart-safe**: on crash, resume from last committed cursor, not from memory.
- **Reorg handling**: support N-block confirmation window. Do not process blocks until `head - confirmBlocks`.
- **Backfill support**: ability to re-index a block range without duplication.

```go
type Indexer struct {
    client    chain.Client   // interface, not *ethclient.Client — see Multi-Chain Readiness
    db        *db.Store
    contracts []ContractConfig
    cursor    uint64
}

func (idx *Indexer) Run(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return nil
        default:
        }

        head, err := idx.client.Head(ctx)
        if err != nil {
            log.Warn().Err(err).Msg("failed to get head block")
            time.Sleep(5 * time.Second)
            continue
        }

        safeHead := head - idx.confirmBlocks
        if safeHead <= idx.cursor {
            time.Sleep(2 * time.Second)
            continue
        }

        toBlock := min(idx.cursor+idx.batchSize, safeHead)
        if err := idx.processBatch(ctx, idx.cursor+1, toBlock); err != nil {
            log.Error().Err(err).Uint64("from", idx.cursor+1).Uint64("to", toBlock).Msg("batch failed")
            time.Sleep(5 * time.Second)
            continue
        }

        idx.cursor = toBlock
        if err := idx.db.SaveCursor(ctx, idx.client.ChainID(), toBlock); err != nil {
            log.Error().Err(err).Msg("failed to persist cursor")
        }
    }
}
```

## ABI Bindings

Generate Go bindings from ABI using `abigen`:

```bash
abigen --abi=out/VaultEngine.sol/VaultEngine.json \
       --pkg=vaultengine \
       --out=pkg/contracts/vault_engine.go
```

Use generated `FilterXxx` methods for event filtering rather than raw log decoding.

## HTTP API (REST)

Use `net/http` + `chi` router. No heavy frameworks.

```go
r := chi.NewRouter()
r.Use(middleware.RequestID)
r.Use(middleware.RealIP)
r.Use(middleware.Recoverer)
r.Use(middleware.Timeout(30 * time.Second))

r.Get("/v1/vault/positions/{address}", h.GetVaultPosition)
r.Get("/v1/oracle/latest", h.GetLatestOracleData)
r.Get("/v1/governance/proposals", h.ListProposals)
```

- All handlers return `application/json`.
- Pagination via `limit`/`offset` query params, capped at 100.
- Errors follow a consistent struct: `{"error": "message", "code": "ERROR_CODE"}`.
- Request validation before any DB access.

## WebSocket API

For real-time feeds (oracle prices, block events):

```go
type Hub struct {
    clients    map[*Client]bool
    broadcast  chan []byte
    register   chan *Client
    unregister chan *Client
}

// Clients subscribe to topics: "oracle:ETH/USD", "vault:deposits", "governance:proposals"
```

## Concurrency Patterns

- Use `errgroup.Group` for parallel goroutines that must all succeed.
- Pass `context.Context` as first arg to every blocking function.
- Never share mutable state between goroutines without a mutex or channel.
- Use `sync.Map` sparingly — prefer explicit mutexes for clarity.
- Worker pools for CPU-bound tasks (e.g., signature verification): bounded goroutine count via semaphore.

```go
sem := make(chan struct{}, workerCount)
for _, item := range items {
    sem <- struct{}{}
    go func(item Item) {
        defer func() { <-sem }()
        process(item)
    }(item)
}
```

## Database Access Layer

- Use `pgx/v5` directly. No ORM.
- Queries defined as constants in the package they belong to.
- All queries parameterized: `$1`, `$2`, etc. Never string concatenation.
- Transactions explicitly managed; rollback on all error paths.

```go
func (s *Store) InsertOracleSubmission(ctx context.Context, sub OracleSubmission) error {
    _, err := s.pool.Exec(ctx, `
        INSERT INTO oracle_submissions (round_id, node_id, value, signature, submitted_at)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (round_id, node_id) DO NOTHING
    `, sub.RoundID, sub.NodeID, sub.Value, sub.Signature, sub.SubmittedAt)
    return err
}
```

## Redis Cache Patterns

Namespace all keys: `pb:{module}:{chain_id}:{entity}:{id}` for chain-derived data,
`pb:{module}:{entity}:{id}` for purely off-chain data. See `database.md` "Redis Key Schema".

```
pb:oracle:42161:latest:ETH-USD         → latest aggregated price
pb:vault:42161:position:{address}      → cached vault position
pb:governance:42161:proposal:{id}      → proposal metadata
```

- TTL on every key. No permanent cache entries.
- Cache-aside pattern: read from Redis, fall back to Postgres, write back to Redis.
- On cache miss under high load: use `SETNX` to prevent thundering herd (single filler pattern).
- Never cache sensitive computed state that must be authoritative (e.g., slashing decisions).
