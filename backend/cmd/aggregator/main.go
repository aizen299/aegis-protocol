// Command aggregator analyses settled oracle rounds and submits the penalties it decides.
//
// It is the first process in this protocol to hold a key that can move value, so the key is
// resolved under the policy in docs/v0.2-oracle-plan.md §2.7 before anything else is built: the
// environment is declared rather than inferred, the source is fixed by that environment, and any
// failure to obtain usable material stops startup rather than falling back.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/chain/evm"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/internal/observability"
	"github.com/aizen299/aegis-protocol/backend/internal/oracle"
	"github.com/aizen299/aegis-protocol/backend/internal/secrets"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const serviceName = "aggregator"

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	log := observability.NewLogger(serviceName, cfg.LogSvc).
		With().Str("environment", cfg.Environment).Logger()

	if err := cfg.ValidateAggregator(); err != nil {
		log.Fatal().Err(err).Msg("invalid configuration")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	key, provider, err := resolveSlasherKey(ctx, cfg)
	if err != nil {
		// The error names the source and the reason, never the material.
		log.Fatal().Err(err).Msg("could not obtain the slasher key")
	}
	log.Info().
		Str("key_provider", provider).
		Str("signer", key.Address()).
		Msg("slasher key resolved")

	store, err := db.New(ctx, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("database unavailable")
	}
	defer store.Close()

	stakingAddress, err := types.IdentityFromEVMHex(cfg.Contracts.OracleStaking)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid CONTRACT_ORACLE_STAKING")
	}
	roundsAddress, err := types.IdentityFromEVMHex(cfg.Contracts.OracleRounds)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid CONTRACT_ORACLE_ROUNDS")
	}

	client, err := evm.New(ctx, evm.Options{
		RPCURL:            cfg.Chain.RPCURL,
		ChainID:           cfg.Chain.ChainID,
		ConfirmationDepth: cfg.Chain.ConfirmBlocks,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("chain client unavailable")
	}
	defer client.Close()

	slasher, err := evm.NewSlasher(client, stakingAddress, key, 0)
	if err != nil {
		log.Fatal().Err(err).Msg("could not build the slasher")
	}

	aggregator := oracle.NewAggregator(store, evm.NewSubmissionVerifier(roundsAddress), log,
		oracle.AggregatorOptions{
			ChainID:        cfg.Chain.ChainID,
			RoundsContract: roundsAddress,
		})
	executor := oracle.NewExecutor(store, slasher, log, oracle.ExecutorOptions{
		ChainID: cfg.Chain.ChainID,
	})

	log.Info().Msg("aggregator started")
	run(ctx, log, aggregator, executor, cfg.Chain.PollInterval)
	log.Info().Msg("aggregator stopped")
}

// resolveSlasherKey obtains the key and proves it is usable before the service does anything else.
//
// The validator parses the material into a signing key, so malformed content fails startup here
// rather than surfacing later as a transaction that cannot be signed — at which point a penalty is
// already overdue and the cause is several layers away.
func resolveSlasherKey(ctx context.Context, cfg *config.Config) (evm.NodeKey, string, error) {
	environment := secrets.Environment(cfg.Environment)

	providers, err := secrets.BuildProviders(ctx, environment, cfg.AWSRegion)
	if err != nil {
		return evm.NodeKey{}, "", err
	}

	var key evm.NodeKey
	validate := func(s secrets.Secret) error {
		parsed, err := evm.NodeKeyFromHex(s.Expose())
		if err != nil {
			return err
		}
		key = parsed
		return nil
	}

	_, provider, err := secrets.Resolve(ctx, secrets.Config{
		Environment: environment,
		Ref:         cfg.Secrets.SlasherKeyRef,
	}, providers, validate)
	if err != nil {
		return evm.NodeKey{}, provider, err
	}
	return key, provider, nil
}

// run alternates analysis and execution. Deciding first means a penalty is durable before the step
// that could crash while submitting it.
func run(ctx context.Context, log zerolog.Logger, aggregator *oracle.Aggregator, executor *oracle.Executor, interval time.Duration) {
	if interval == 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		analysed, err := aggregator.Step(ctx)
		if err != nil {
			log.Error().Err(err).Msg("analysis failed")
		}

		submitted, err := executor.Step(ctx)
		if err != nil {
			log.Error().Err(err).Msg("execution failed")
		}
		if analysed > 0 || submitted > 0 {
			log.Info().Int("rounds_analysed", analysed).Int("slashes_submitted", submitted).Msg("cycle complete")
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
