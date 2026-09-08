//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/api"
	"github.com/aizen299/aegis-protocol/backend/internal/cache"
	"github.com/aizen299/aegis-protocol/backend/internal/chain/evm"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/internal/indexer"
	"github.com/aizen299/aegis-protocol/backend/internal/vault"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/vaultengine"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// Deposit 1.5 tUSD of a six-decimal token. Raw base units, so 1_500_000.
const (
	depositRaw     = "1500000"
	withdrawShares = "750000000" // half the shares minted by that deposit
)

type stack struct {
	deployment deployment
	store      *db.Store
	cache      *cache.Client
	client     *evm.Client
	indexer    *indexer.Indexer
	server     http.Handler
	cfg        *config.Config
}

func setupStack(t *testing.T) *stack {
	t.Helper()
	requireDeps(t)

	ctx := context.Background()
	d := deployFresh(t)

	cfg := &config.Config{}
	cfg.DB.DSN = envOr("DB_DSN", "postgres://pb:pb_local@localhost:5432/aegis?sslmode=disable")
	cfg.DB.MaxOpenConns = 8
	cfg.DB.MinConns = 1
	cfg.Redis.Addr = envOr("REDIS_ADDR", "localhost:6379")
	cfg.Chain.ChainID = chainID
	cfg.API.MaxPageSize = 100
	cfg.API.WriteTimeout = 30 * time.Second

	store, err := db.New(ctx, cfg)
	if err != nil {
		t.Skipf("postgres unavailable: %v (run `make e2e`)", err)
	}
	t.Cleanup(store.Close)

	redis, err := cache.New(ctx, cfg)
	if err != nil {
		t.Skipf("redis unavailable: %v (run `make e2e`)", err)
	}
	t.Cleanup(func() { _ = redis.Close() })

	truncate(t, cfg.DB.DSN)

	vaultID, err := types.IdentityFromEVMHex(d.VaultProxy)
	if err != nil {
		t.Fatalf("vault address: %v", err)
	}

	// Confirmation depth 1 keeps the test fast while still exercising the reorg-safety path.
	client, err := evm.New(ctx, evm.Options{
		RPCURL:            anvilRPC,
		ChainID:           chainID,
		ConfirmationDepth: 1,
		Contracts:         []evm.Registration{{Address: vaultID, ABI: vaultengine.ABI()}},
	})
	if err != nil {
		t.Fatalf("evm client: %v", err)
	}
	t.Cleanup(client.Close)

	log := zerolog.New(io.Discard)
	idx := indexer.New(client, store, log, indexer.Options{
		ServiceName: "e2e",
		StartBlock:  d.DeployedAtBlock,
		BatchSize:   500,
	}, indexer.NewVaultHandler(store, client, vaultID))

	if err := idx.Restore(ctx); err != nil {
		t.Fatalf("restore cursor: %v", err)
	}

	srv := api.NewServer(cfg, log, api.Deps{
		Store:   store,
		Cache:   redis,
		Vault:   vault.NewService(store, redis, log, chainID),
		Chain:   client,
		ChainID: chainID,
	})

	return &stack{
		deployment: d, store: store, cache: redis, client: client,
		indexer: idx, server: srv.Handler(), cfg: cfg,
	}
}

// indexToHead runs the indexer until it has caught up with the chain.
func (s *stack) indexToHead(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	mineBlocks(t, 2)
	head := headBlock(t)

	deadline := time.Now().Add(30 * time.Second)
	for s.indexer.Cursor() < head-1 {
		if time.Now().After(deadline) {
			t.Fatalf("indexer stalled at cursor %d, head %d", s.indexer.Cursor(), head)
		}
		advanced, err := s.indexer.Step(ctx)
		if err != nil {
			t.Fatalf("indexer step: %v", err)
		}
		if !advanced {
			break
		}
	}
}

// replayFromGenesis drives the indexer over a range it has already processed.
func (s *stack) replayFromGenesis(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	if err := s.indexer.Restore(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for {
		advanced, err := s.indexer.Step(ctx)
		if err != nil {
			t.Fatalf("replay step: %v", err)
		}
		if !advanced {
			return
		}
	}
}

func (s *stack) get(t *testing.T, path string) (int, map[string]any) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	s.server.ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v\nbody: %s", path, err, rec.Body.String())
	}
	return rec.Code, body
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
