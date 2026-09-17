// Command api serves the REST surface over indexed state.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/aizen299/aegis-protocol/backend/internal/api"
	"github.com/aizen299/aegis-protocol/backend/internal/cache"
	"github.com/aizen299/aegis-protocol/backend/internal/chain/evm"
	"github.com/aizen299/aegis-protocol/backend/internal/chain/svm"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/internal/governance"
	"github.com/aizen299/aegis-protocol/backend/internal/observability"
	"github.com/aizen299/aegis-protocol/backend/internal/oracle"
	"github.com/aizen299/aegis-protocol/backend/internal/vault"
	"github.com/aizen299/aegis-protocol/backend/internal/zk"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const serviceName = "api"

func main() {
	cfg, err := config.Load()
	if err != nil {
		// The logger does not exist yet, and a stack trace is noise for what is always a
		// misconfiguration. Say what is wrong and exit.
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	chains, err := cfg.APIChains()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	log := observability.NewLogger(serviceName, cfg.LogSvc).
		With().Str("environment", cfg.Environment).Logger()

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

	// The API reads no chain state, so it opens no RPC connection: an address needs only its chain's
	// encoding. The governor and zk are not on Solana, so they are left nil and answer 404 there; Solana
	// serves the governance actions it received instead. §10.8, §18.8.
	deps := make([]api.ChainDeps, 0, len(chains))
	for _, c := range chains {
		d := api.ChainDeps{ID: c.ID, Vault: vault.NewService(store, redis, log, c.ID)}
		switch c.VM {
		case types.VMSVM:
			d.Codec = svm.Codec{}
			d.Oracle = oracle.NewService(store, redis, log, c.ID)
			d.RemoteGovernance = governance.NewRemoteService(store, c.ID)
		default:
			d.Codec = evm.Codec{}
			d.Oracle = oracle.NewService(store, redis, log, c.ID)
			d.Governance = governance.NewService(store, redis, log, c.ID)
			d.Zk = zk.NewService(store, redis, log, c.ID)
		}
		deps = append(deps, d)
		log.Info().Str("chain", c.Name).Int64("chain_id", c.ID).Msg("serving chain")
	}

	srv := api.NewServer(cfg, log, api.Deps{
		Store:   store,
		Cache:   redis,
		Chains:  deps,
		Metrics: observability.NewMetrics(serviceName, cfg.Environment),
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
