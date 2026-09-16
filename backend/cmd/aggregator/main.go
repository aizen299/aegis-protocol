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
	"github.com/aizen299/aegis-protocol/backend/internal/chain/svm"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/internal/observability"
	"github.com/aizen299/aegis-protocol/backend/internal/oracle"
	"github.com/aizen299/aegis-protocol/backend/internal/secrets"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/soloracle"
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

	store, err := db.New(ctx, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("database unavailable")
	}
	defer store.Close()

	var verifier oracle.SignatureVerifier
	var slasher oracle.Slasher
	if info, _ := types.LookupChain(cfg.Chain.ChainID); info.VM == types.VMSVM {
		verifier, slasher = solanaAdapters(ctx, cfg, log)
	} else {
		verifier, slasher = evmAdapters(ctx, cfg, log)
	}

	aggregator := oracle.NewAggregator(store, verifier, log, oracle.AggregatorOptions{ChainID: cfg.Chain.ChainID})
	executor := oracle.NewExecutor(store, slasher, log, oracle.ExecutorOptions{
		ChainID: cfg.Chain.ChainID,
	})

	log.Info().Msg("aggregator started")
	run(ctx, log, aggregator, executor, cfg.Chain.PollInterval)
	log.Info().Msg("aggregator stopped")
}

// evmAdapters builds EIP-712 verification and the SLASHER_ROLE slasher. The client stays open for
// the life of the process.
func evmAdapters(ctx context.Context, cfg *config.Config, log zerolog.Logger) (oracle.SignatureVerifier, oracle.Slasher) {
	key, provider, err := resolveSlasherKey(ctx, cfg)
	if err != nil {
		// The error names the source and the reason, never the material.
		log.Fatal().Err(err).Msg("could not obtain the slasher key")
	}
	log.Info().Str("key_provider", provider).Str("signer", key.Address()).Msg("slasher key resolved")

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
	slasher, err := evm.NewSlasher(client, stakingAddress, key, 0)
	if err != nil {
		log.Fatal().Err(err).Msg("could not build the slasher")
	}
	return evm.NewSubmissionVerifier(roundsAddress), slasher
}

// solanaAdapters builds ed25519 verification and the Solana slasher. The slasher key goes through the
// same providers and no-fallback policy, and is accepted only in solana-keygen's JSON form. §12.8.
func solanaAdapters(ctx context.Context, cfg *config.Config, log zerolog.Logger) (oracle.SignatureVerifier, oracle.Slasher) {
	if err := cfg.ValidateSolanaContracts(); err != nil {
		log.Fatal().Err(err).Msg("invalid configuration")
	}
	var key svm.Keypair
	provider, err := resolveSecret(ctx, cfg, func(s secrets.Secret) error {
		parsed, err := svm.KeypairFromJSON(s.Expose())
		key = parsed
		return err
	})
	if err != nil {
		log.Fatal().Err(err).Msg("could not obtain the slasher key")
	}
	log.Info().Str("key_provider", provider).Str("signer", svm.Encode(key.Identity())).Msg("slasher key resolved")

	program, err := svm.Decode(cfg.Contracts.OracleStaking)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid CONTRACT_ORACLE_STAKING")
	}
	idl, err := svm.ParseIDL(soloracle.IDL)
	if err != nil {
		log.Fatal().Err(err).Msg("embedded oracle idl is invalid")
	}
	if idl.Program != program {
		log.Fatal().Msg("CONTRACT_ORACLE_STAKING is not the program the embedded oracle idl describes")
	}
	client, err := svm.New(ctx, svm.Options{RPCURL: cfg.Chain.RPCURL, ChainID: cfg.Chain.ChainID, Programs: []svm.Registration{{IDL: idl}}})
	if err != nil {
		log.Fatal().Err(err).Msg("chain client unavailable")
	}
	slasher, err := svm.NewSlasher(client, program, key)
	if err != nil {
		log.Fatal().Err(err).Msg("could not build the slasher")
	}
	return svm.NewSubmissionVerifier(program), slasher
}

// resolveSlasherKey obtains the key and proves it is usable before the service does anything else.
//
// The validator parses the material into a signing key, so malformed content fails startup here
// rather than surfacing later as a transaction that cannot be signed — at which point a penalty is
// already overdue and the cause is several layers away.
func resolveSlasherKey(ctx context.Context, cfg *config.Config) (evm.NodeKey, string, error) {
	var key evm.NodeKey
	provider, err := resolveSecret(ctx, cfg, func(s secrets.Secret) error {
		parsed, err := evm.NodeKeyFromHex(s.Expose())
		key = parsed
		return err
	})
	return key, provider, err
}

// resolveSecret fetches SLASHER_KEY_REF through the environment's providers. validate parses the
// value, so malformed material fails startup like missing material would.
func resolveSecret(ctx context.Context, cfg *config.Config, validate func(secrets.Secret) error) (string, error) {
	environment := secrets.Environment(cfg.Environment)
	providers, err := secrets.BuildProviders(ctx, environment, cfg.AWSRegion)
	if err != nil {
		return "", err
	}
	_, provider, err := secrets.Resolve(ctx, secrets.Config{
		Environment: environment,
		Ref:         cfg.Secrets.SlasherKeyRef,
	}, providers, validate)
	return provider, err
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

		// Before execution, so a missed-round decision reaches the chain in the same cycle it is made.
		judged, err := aggregator.JudgeMisses(ctx)
		if err != nil {
			log.Error().Err(err).Msg("missed-round judgement failed")
		}

		submitted, err := executor.Step(ctx)
		if err != nil {
			log.Error().Err(err).Msg("execution failed")
		}
		if analysed > 0 || judged > 0 || submitted > 0 {
			log.Info().
				Int("rounds_analysed", analysed).
				Int("rounds_judged_for_misses", judged).
				Int("slashes_submitted", submitted).
				Msg("cycle complete")
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
