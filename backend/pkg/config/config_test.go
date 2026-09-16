package config

import (
	"os"
	"strings"
	"testing"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// unsetEnv removes a variable for the duration of the test and restores it afterwards.
//
// t.Setenv cannot unset, but it does register the restore, so setting then unsetting leaves cleanup
// correct. Needed because a test asserting on an absent variable must control it: this suite passed
// locally and failed in CI purely because the CI job sets APP_ENV.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "placeholder")
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
}

// APP_ENV has no default on purpose. A default would mean an unconfigured process decides it is in
// development, which is the inference the secret policy exists to forbid.
func TestEnvironmentIsRequired(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://localhost/test")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	unsetEnv(t, "APP_ENV")

	if _, err := Load(); err == nil {
		t.Fatal("config loaded without APP_ENV; the environment must be declared, not defaulted")
	} else if !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("err = %v, should name the missing variable", err)
	}
}

// `required` only checks presence, so an empty value satisfies it. Rejecting it at load stops a
// service starting, connecting to a database, and only then discovering it cannot say which
// environment it is in.
func TestEmptyEnvironmentIsRejected(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://localhost/test")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("APP_ENV", "")

	if _, err := Load(); err == nil {
		t.Fatal("config loaded with an empty APP_ENV")
	}
}

func TestUnknownEnvironmentIsRejectedAtLoad(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://localhost/test")
	t.Setenv("REDIS_ADDR", "localhost:6379")

	for _, value := range []string{"dev", "prod", "Staging", "test"} {
		t.Setenv("APP_ENV", value)
		if _, err := Load(); err == nil {
			t.Errorf("APP_ENV=%q loaded; only local, staging, production are valid", value)
		}
	}
}

func TestEnvironmentIsLoaded(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://localhost/test")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("APP_ENV", "staging")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Environment != "staging" {
		t.Fatalf("environment = %q", cfg.Environment)
	}
}

func TestValidateAggregatorRequiresKeyReference(t *testing.T) {
	cfg := &Config{}
	cfg.Chain.ChainID = types.ChainIDAnvil
	cfg.Contracts.OracleStaking = "0x0000000000000000000000000000000000000001"

	if err := cfg.ValidateAggregator(); err == nil {
		t.Fatal("aggregator validated without SLASHER_KEY_REF")
	}

	cfg.Secrets.SlasherKeyRef = "/aegis/staging/slasher_key"
	if err := cfg.ValidateAggregator(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// A node configured with fewer sources than its own floor can never submit. Failing at startup
// beats discovering it when a round is already open.
func TestValidateNodeRejectsFewerSourcesThanTheFloor(t *testing.T) {
	cfg := &Config{}
	cfg.Chain.ChainID = types.ChainIDAnvil
	cfg.Secrets.NodeKeyRef = "NODE_PRIVATE_KEY"
	cfg.Contracts.OracleRounds = "0x1"
	cfg.Contracts.OracleStaking = "0x2"
	cfg.Node.FeedName = "ETH/USD"
	cfg.Node.Sources = []string{"https://a", "https://b"}
	cfg.Node.SourcePaths = []string{"price", "price"}
	cfg.Node.MinSources = 3

	if err := cfg.ValidateNode(); err == nil {
		t.Fatal("a node with two sources and a floor of three validated")
	}
}

// Sources and their JSON paths are positional, so a length mismatch is a misconfiguration that
// would otherwise read the wrong path from the wrong endpoint.
func TestValidateNodeRejectsMismatchedSourcePaths(t *testing.T) {
	cfg := &Config{}
	cfg.Chain.ChainID = types.ChainIDAnvil
	cfg.Secrets.NodeKeyRef = "NODE_PRIVATE_KEY"
	cfg.Contracts.OracleRounds = "0x1"
	cfg.Contracts.OracleStaking = "0x2"
	cfg.Node.FeedName = "ETH/USD"
	cfg.Node.Sources = []string{"https://a", "https://b", "https://c"}
	cfg.Node.SourcePaths = []string{"price"}
	cfg.Node.MinSources = 3

	if err := cfg.ValidateNode(); err == nil {
		t.Fatal("mismatched sources and paths validated")
	}
}

func TestValidateNodeAcceptsACompleteConfiguration(t *testing.T) {
	cfg := &Config{}
	cfg.Chain.ChainID = types.ChainIDAnvil
	cfg.Secrets.NodeKeyRef = "NODE_PRIVATE_KEY"
	cfg.Contracts.OracleRounds = "0x1"
	cfg.Contracts.OracleStaking = "0x2"
	cfg.Node.FeedName = "ETH/USD"
	cfg.Node.Sources = []string{"https://a", "https://b", "https://c"}
	cfg.Node.SourcePaths = []string{"price", "price", "data.amount"}
	cfg.Node.MinSources = 3

	if err := cfg.ValidateNode(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// A mistyped CHAIN_ID would not fail on its own: it would index into rows no other service reads.
func TestAnUnassignedChainIDIsRefused(t *testing.T) {
	cfg := &Config{}
	cfg.Secrets.SlasherKeyRef = "SLASHER_PRIVATE_KEY"
	cfg.Contracts.OracleStaking = "0x1"

	for _, id := range []int64{0, 31338, types.NonEVMChainIDBase} {
		cfg.Chain.ChainID = id
		if err := cfg.ValidateChain(); err == nil {
			t.Errorf("chain ID %d validated", id)
		}
		if err := cfg.ValidateAggregator(); err == nil {
			t.Errorf("aggregator validated with chain ID %d", id)
		}
	}
	cfg.Chain.ChainID = types.ChainIDSolanaLocalnet
	if err := cfg.ValidateChain(); err != nil {
		t.Errorf("solana-localnet refused: %v", err)
	}
}

func TestAPIChainsDefaultToTheConfiguredChain(t *testing.T) {
	cfg := &Config{}
	cfg.Chain.ChainID = types.ChainIDAnvil
	chains, err := cfg.APIChains()
	if err != nil || len(chains) != 1 || chains[0].ID != types.ChainIDAnvil {
		t.Fatalf("chains = %v, err = %v", chains, err)
	}

	cfg.Chain.ChainID = 0
	if _, err := cfg.APIChains(); err == nil {
		t.Fatal("no API_CHAINS and no CHAIN_ID resolved to a chain")
	}
}

func TestAPIChainsRefuseUnknownAndRepeatedNames(t *testing.T) {
	cfg := &Config{}
	cfg.API.Chains = []string{"anvil", "solana-localnet"}
	if chains, err := cfg.APIChains(); err != nil || len(chains) != 2 {
		t.Fatalf("chains = %v, err = %v", chains, err)
	}
	for _, names := range [][]string{{"anvil", "anvil"}, {"anvil", "solana"}, {""}} {
		cfg.API.Chains = names
		if _, err := cfg.APIChains(); err == nil {
			t.Errorf("%v accepted", names)
		}
	}
}
