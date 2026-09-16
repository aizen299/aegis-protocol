// Command oraclenode is one participant in the oracle network.
//
// It fetches a price from several independent sources, takes the median across them, signs it, and
// submits it to the round that is open. It holds its own key — not SLASHER_ROLE — under the same
// sourcing policy, because a node key can stake, unstake, and sign submissions attributed to that
// node, and none of those should ever come from a development variable in staging.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/chain/evm"
	"github.com/aizen299/aegis-protocol/backend/internal/chain/svm"
	"github.com/aizen299/aegis-protocol/backend/internal/observability"
	"github.com/aizen299/aegis-protocol/backend/internal/oraclenode"
	"github.com/aizen299/aegis-protocol/backend/internal/secrets"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/soloracle"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const serviceName = "oraclenode"

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	log := observability.NewLogger(serviceName, cfg.LogSvc).
		With().Str("environment", cfg.Environment).Logger()

	if err := cfg.ValidateNode(); err != nil {
		log.Fatal().Err(err).Msg("invalid configuration")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var chain oraclenode.Chain
	var feedID [32]byte
	if info, _ := types.LookupChain(cfg.Chain.ChainID); info.VM == types.VMSVM {
		chain, feedID = solanaChain(ctx, cfg, log)
	} else {
		chain, feedID = evmChain(ctx, cfg, log)
	}

	node := oraclenode.NewNode(chain, buildFetcher(cfg, log), log, oraclenode.NodeOptions{
		FeedID:       "0x" + fmt.Sprintf("%x", feedID[:]),
		PollInterval: cfg.Chain.PollInterval,
		SubmitBefore: cfg.Node.SubmitBefore,
	})

	log.Info().
		Str("feed", cfg.Node.FeedName).
		Int("sources", len(cfg.Node.Sources)).
		Int("min_sources", cfg.Node.MinSources).
		Msg("oracle node started")

	if err := node.Run(ctx); err != nil {
		log.Error().Err(err).Msg("node stopped with error")
		os.Exit(1)
	}
	log.Info().Msg("oracle node stopped")
}

func buildFetcher(cfg *config.Config, log zerolog.Logger) *oraclenode.Fetcher {
	client := &http.Client{Timeout: 10 * time.Second}

	sources := make([]oraclenode.Source, 0, len(cfg.Node.Sources))
	for i, url := range cfg.Node.Sources {
		sources = append(sources, &oraclenode.HTTPSource{
			SourceName: fmt.Sprintf("source-%d", i+1),
			URL:        url,
			Path:       cfg.Node.SourcePaths[i],
			Client:     client,
		})
	}

	return oraclenode.NewFetcher(sources, log, oraclenode.FetcherOptions{
		MinSources: cfg.Node.MinSources,
		MaxRetries: cfg.Node.MaxRetries,
	})
}

// evmChain builds the Arbitrum adapter. The client is left open for the life of the process.
func evmChain(ctx context.Context, cfg *config.Config, log zerolog.Logger) (oraclenode.Chain, [32]byte) {
	key, provider, err := resolveNodeKey(ctx, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("could not obtain the node key")
	}
	log.Info().Str("key_provider", provider).Str("node", key.Address()).Msg("node key resolved")

	roundsAddress, err := types.IdentityFromEVMHex(cfg.Contracts.OracleRounds)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid CONTRACT_ORACLE_ROUNDS")
	}
	stakingAddress, err := types.IdentityFromEVMHex(cfg.Contracts.OracleStaking)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid CONTRACT_ORACLE_STAKING")
	}

	client, err := evm.New(ctx, evm.Options{
		RPCURL:            cfg.Chain.RPCURL,
		ChainID:           cfg.Chain.ChainID,
		ConfirmationDepth: cfg.Chain.ConfirmBlocks,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("chain client unavailable")
	}

	chain, err := evm.NewOracleNodeChain(client, roundsAddress, stakingAddress, key, 0)
	if err != nil {
		log.Fatal().Err(err).Msg("could not build the chain adapter")
	}
	return chain, evm.FeedID(cfg.Node.FeedName)
}

// solanaChain builds the Solana adapter. The key comes through the same providers and no-fallback
// policy as on Arbitrum, and is accepted only in solana-keygen's JSON form. §12.8.
func solanaChain(ctx context.Context, cfg *config.Config, log zerolog.Logger) (oraclenode.Chain, [32]byte) {
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
		log.Fatal().Err(err).Msg("could not obtain the node key")
	}
	log.Info().Str("key_provider", provider).Str("node", svm.Encode(key.Identity())).Msg("node key resolved")

	program, err := svm.Decode(cfg.Contracts.OracleRounds)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid CONTRACT_ORACLE_ROUNDS")
	}
	idl, err := svm.ParseIDL(soloracle.IDL)
	if err != nil {
		log.Fatal().Err(err).Msg("embedded oracle idl is invalid")
	}
	if idl.Program != program {
		log.Fatal().Msg("CONTRACT_ORACLE_ROUNDS is not the program the embedded oracle idl describes")
	}

	client, err := svm.New(ctx, svm.Options{RPCURL: cfg.Chain.RPCURL, ChainID: cfg.Chain.ChainID, Programs: []svm.Registration{{IDL: idl}}})
	if err != nil {
		log.Fatal().Err(err).Msg("chain client unavailable")
	}
	chain, err := svm.NewOracleNodeChain(client, key)
	if err != nil {
		log.Fatal().Err(err).Msg("could not build the chain adapter")
	}
	return chain, svm.FeedID(cfg.Node.FeedName)
}

// resolveNodeKey obtains the node's signing key under the same policy as the slasher key. A node
// key is not a lesser secret: it can stake, unstake, and produce submissions attributed to this
// node, and a submission it did not intend is slashable.
func resolveNodeKey(ctx context.Context, cfg *config.Config) (evm.NodeKey, string, error) {
	var key evm.NodeKey
	provider, err := resolveSecret(ctx, cfg, func(s secrets.Secret) error {
		parsed, err := evm.NodeKeyFromHex(s.Expose())
		key = parsed
		return err
	})
	return key, provider, err
}

// resolveSecret fetches the node key reference through the environment's providers. validate runs
// on the fetched value; a value it refuses fails startup like a missing one would.
func resolveSecret(ctx context.Context, cfg *config.Config, validate func(secrets.Secret) error) (string, error) {
	environment := secrets.Environment(cfg.Environment)
	providers, err := secrets.BuildProviders(ctx, environment, cfg.AWSRegion)
	if err != nil {
		return "", err
	}
	_, provider, err := secrets.Resolve(ctx, secrets.Config{
		Environment: environment,
		Ref:         cfg.Secrets.NodeKeyRef,
	}, providers, validate)
	return provider, err
}
