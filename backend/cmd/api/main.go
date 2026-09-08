// Command api serves the REST surface over indexed state.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/aizen299/aegis-protocol/backend/internal/api"
	"github.com/aizen299/aegis-protocol/backend/internal/cache"
	"github.com/aizen299/aegis-protocol/backend/internal/chain/evm"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/internal/observability"
	"github.com/aizen299/aegis-protocol/backend/internal/vault"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
)

const serviceName = "api"

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	log := observability.NewLogger(serviceName, cfg.LogSvc)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := db.New(ctx, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("database unavailable")
	}
	defer store.Close()

	redis, err := cache.New(ctx, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("cache unavailable")
	}
	defer redis.Close()

	// The API reads no chain state; it uses the client purely for this chain's identity encoding.
	client, err := evm.New(ctx, evm.Options{
		RPCURL:            cfg.Chain.RPCURL,
		ChainID:           cfg.Chain.ChainID,
		ConfirmationDepth: cfg.Chain.ConfirmBlocks,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("chain client unavailable")
	}
	defer client.Close()

	srv := api.NewServer(cfg, log, api.Deps{
		Store:   store,
		Cache:   redis,
		Vault:   vault.NewService(store, redis, log, cfg.Chain.ChainID),
		Chain:   client,
		ChainID: cfg.Chain.ChainID,
	})

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	select {
	case err := <-errCh:
		if err != nil {
			log.Error().Err(err).Msg("api stopped with error")
			os.Exit(1)
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.API.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Dur("timeout", cfg.API.ShutdownTimeout).Msg("graceful shutdown failed")
		os.Exit(1)
	}
	log.Info().Msg("api stopped")
}
