// Command indexer consumes chain events into Postgres. One process serves one chain.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	"github.com/aizen299/aegis-protocol/backend/internal/chain/evm"
	"github.com/aizen299/aegis-protocol/backend/internal/chain/svm"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/internal/indexer"
	"github.com/aizen299/aegis-protocol/backend/internal/observability"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/governance"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/oracle"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/soloracle"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/solvault"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/vaultengine"
	zkcontracts "github.com/aizen299/aegis-protocol/backend/pkg/contracts/zk"
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

	chainInfo, _ := types.LookupChain(cfg.Chain.ChainID)
	var client chain.Client
	var handlers []indexer.Handler
	switch chainInfo.VM {
	case types.VMSVM:
		client, handlers = solanaHandlers(ctx, cfg, store, log)
	default:
		client, handlers = evmHandlers(ctx, cfg, store, log)
	}
	defer client.Close()

	log.Info().
		Bool("oracle", cfg.OracleEnabled()).
		Bool("governance", cfg.GovernanceEnabled()).
		Bool("zk", cfg.ZkEnabled()).
		Int("handlers", len(handlers)).
		Msg("handlers wired")

	idx := indexer.New(client, store, log, indexer.Options{
		ServiceName:  serviceName,
		StartBlock:   cfg.Chain.StartBlock,
		BatchSize:    cfg.Chain.BatchSize,
		PollInterval: cfg.Chain.PollInterval,
		Metrics:      observability.NewMetrics(serviceName, cfg.Environment),
	}, handlers...)

	if err := idx.Run(ctx); err != nil {
		log.Error().Err(err).Msg("indexer stopped with error")
		os.Exit(1)
	}

	log.Info().Uint64("cursor", idx.Cursor()).Msg("indexer stopped")
}

// evmHandlers builds the EVM client and every handler its configured contracts need.
func evmHandlers(ctx context.Context, cfg *config.Config, store *db.Store, log zerolog.Logger) (chain.Client, []indexer.Handler) {
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

	zkTree, err := optionalAddress(cfg.Contracts.ZkTree)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid CONTRACT_ZK_TREE")
	}
	zkGate, err := optionalAddress(cfg.Contracts.ZkGate)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid CONTRACT_ZK_GATE")
	}
	if zkTree != nil && zkGate != nil {
		registrations = append(registrations,
			evm.Registration{Address: *zkTree, ABI: zkcontracts.TreeABI()},
			evm.Registration{Address: *zkGate, ABI: zkcontracts.GateABI()})
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
	if zkTree != nil && zkGate != nil {
		handlers = append(handlers,
			indexer.NewZkHandler(store, client, evm.NewZkReader(client), *zkTree, *zkGate))
	}

	return client, handlers
}

// solanaHandlers builds the Solana client. Only the vault exists on Solana so far, so a configured
// oracle, governance, or zk contract is refused rather than silently not indexed.
func solanaHandlers(ctx context.Context, cfg *config.Config, store *db.Store, log zerolog.Logger) (chain.Client, []indexer.Handler) {
	if err := cfg.ValidateSolanaContracts(); err != nil {
		log.Fatal().Err(err).Msg("invalid configuration")
	}

	// Each program's IDL decodes by discriminator, so a configured id that is not the program the IDL
	// describes would have another program's events decoded as ours.
	programFor := func(value, env string, raw []byte) (types.Identity, *svm.IDL) {
		program, err := svm.Decode(value)
		if err != nil {
			log.Fatal().Err(err).Str("value", value).Msg("invalid " + env)
		}
		idl, err := svm.ParseIDL(raw)
		if err != nil {
			log.Fatal().Err(err).Msg("embedded idl is invalid")
		}
		if idl.Program != program {
			log.Fatal().Str("configured", value).Str("idl", svm.Encode(idl.Program)).
				Msg(env + " is not the program the embedded idl describes")
		}
		return program, idl
	}

	vaultProgram, vaultIDL := programFor(cfg.Contracts.VaultEngine, "CONTRACT_VAULT_ENGINE", solvault.IDL)
	registrations := []svm.Registration{{IDL: vaultIDL}}

	var oracleProgram, stakeMint types.Identity
	if cfg.OracleEnabled() {
		var oracleIDL *svm.IDL
		oracleProgram, oracleIDL = programFor(cfg.Contracts.OracleRounds, "CONTRACT_ORACLE_ROUNDS", soloracle.IDL)
		registrations = append(registrations, svm.Registration{IDL: oracleIDL})
		mint, err := svm.Decode(cfg.Contracts.OracleStake)
		if err != nil {
			log.Fatal().Err(err).Msg("invalid CONTRACT_ORACLE_STAKE_TOKEN")
		}
		stakeMint = mint
	}

	client, err := svm.New(ctx, svm.Options{
		RPCURL:   cfg.Chain.RPCURL,
		ChainID:  cfg.Chain.ChainID,
		Programs: registrations,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("chain client unavailable")
	}

	handlers := []indexer.Handler{indexer.NewVaultHandler(store, client, svm.NewVaultLocator(client), vaultProgram)}
	if cfg.OracleEnabled() {
		handlers = append(handlers,
			indexer.NewOracleRoundsHandler(store, client, oracleProgram),
			indexer.NewOracleStakingHandler(store, client, oracleProgram, stakeMint))
	}
	return client, handlers
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
