// Command indexer consumes chain events into Postgres. One process serves one chain.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/aizen299/aegis-protocol/backend/internal/chain/evm"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/internal/indexer"
	"github.com/aizen299/aegis-protocol/backend/internal/observability"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/governance"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/oracle"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/vaultengine"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const serviceName = "indexer"

func main() {
	cfg, err := config.Load()
	if err != nil {
		// The logger does not exist yet, and a stack trace is noise for what is always a
		// misconfiguration. Say what is wrong and exit.
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	log := observability.NewLogger(serviceName, cfg.LogSvc).
		With().Str("environment", cfg.Environment).Logger()

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

	registrations := []evm.Registration{{Address: vaultAddress, ABI: vaultengine.ABI()}}

	oracleRounds, err := optionalAddress(cfg.Contracts.OracleRounds)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid CONTRACT_ORACLE_ROUNDS")
	}
	oracleStaking, err := optionalAddress(cfg.Contracts.OracleStaking)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid CONTRACT_ORACLE_STAKING")
	}
	stakeToken, err := optionalAddress(cfg.Contracts.OracleStake)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid CONTRACT_ORACLE_STAKE_TOKEN")
	}

	if oracleRounds != nil {
		registrations = append(registrations, evm.Registration{Address: *oracleRounds, ABI: oracle.RoundsABI()})
	}
	if oracleStaking != nil {
		registrations = append(registrations, evm.Registration{Address: *oracleStaking, ABI: oracle.StakingABI()})
	}

	governorAddress, err := optionalAddress(cfg.Contracts.Governor)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid CONTRACT_GOVERNOR")
	}
	if governorAddress != nil {
		registrations = append(registrations, evm.Registration{Address: *governorAddress, ABI: governance.GovernorABI()})
	}

	client, err := evm.New(ctx, evm.Options{
		RPCURL:            cfg.Chain.RPCURL,
		ChainID:           cfg.Chain.ChainID,
		ConfirmationDepth: cfg.Chain.ConfirmBlocks,
		Contracts:         registrations,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("chain client unavailable")
	}
	defer client.Close()

	handlers := []indexer.Handler{
		indexer.NewVaultHandler(store, client, evm.NewVaultReader(client), vaultAddress),
	}
	if oracleRounds != nil {
		handlers = append(handlers, indexer.NewOracleRoundsHandler(store, client, *oracleRounds))
	}
	if oracleStaking != nil {
		handlers = append(handlers, indexer.NewOracleStakingHandler(store, client, *oracleStaking, *stakeToken))
	}
	if governorAddress != nil {
		handlers = append(handlers,
			indexer.NewGovernanceHandler(store, client, evm.NewGovernanceReader(client), *governorAddress))
	}

	log.Info().
		Bool("oracle", cfg.OracleEnabled()).
		Bool("governance", cfg.GovernanceEnabled()).
		Int("handlers", len(handlers)).
		Msg("handlers wired")

	idx := indexer.New(client, store, log, indexer.Options{
		ServiceName:  serviceName,
		StartBlock:   cfg.Chain.StartBlock,
		BatchSize:    cfg.Chain.BatchSize,
		PollInterval: cfg.Chain.PollInterval,
	}, handlers...)

	if err := idx.Run(ctx); err != nil {
		log.Error().Err(err).Msg("indexer stopped with error")
		os.Exit(1)
	}

	log.Info().Uint64("cursor", idx.Cursor()).Msg("indexer stopped")
}

// optionalAddress parses a contract address that may legitimately be unset.
func optionalAddress(value string) (*types.Identity, error) {
	if value == "" {
		return nil, nil
	}
	id, err := types.IdentityFromEVMHex(value)
	if err != nil {
		return nil, err
	}
	return &id, nil
}
