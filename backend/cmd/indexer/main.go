// Command indexer consumes chain events into Postgres. One process serves one chain.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/aizen299/aegis-protocol/backend/internal/chain/evm"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/internal/indexer"
	"github.com/aizen299/aegis-protocol/backend/internal/observability"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/vaultengine"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const serviceName = "indexer"

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	log := observability.NewLogger(serviceName, cfg.LogSvc)

	if err := cfg.ValidateIndexer(); err != nil {
		log.Fatal().Err(err).Msg("invalid configuration")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := db.New(ctx, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("database unavailable")
	}
	defer store.Close()

	vaultAddress, err := types.IdentityFromEVMHex(cfg.Contracts.VaultEngine)
	if err != nil {
		log.Fatal().Err(err).Str("value", cfg.Contracts.VaultEngine).Msg("invalid vault address")
	}

	client, err := evm.New(ctx, evm.Options{
		RPCURL:            cfg.Chain.RPCURL,
		ChainID:           cfg.Chain.ChainID,
		ConfirmationDepth: cfg.Chain.ConfirmBlocks,
		Contracts: []evm.Registration{
			{Address: vaultAddress, ABI: vaultengine.ABI()},
		},
	})
	if err != nil {
		log.Fatal().Err(err).Msg("chain client unavailable")
	}
	defer client.Close()

	idx := indexer.New(client, store, log, indexer.Options{
		ServiceName:  serviceName,
		StartBlock:   cfg.Chain.StartBlock,
		BatchSize:    cfg.Chain.BatchSize,
		PollInterval: cfg.Chain.PollInterval,
	}, indexer.NewVaultHandler(store, client, vaultAddress))

	if err := idx.Run(ctx); err != nil {
		log.Error().Err(err).Msg("indexer stopped with error")
		os.Exit(1)
	}

	log.Info().Uint64("cursor", idx.Cursor()).Msg("indexer stopped")
}
