package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Service string `env:"SERVICE_NAME" envDefault:"backend"`
	LogSvc  string `env:"LOG_LEVEL" envDefault:"info"`

	// Environment is required and has no default. A default would mean an unconfigured process
	// decides it is in development, which is exactly the inference the secret policy forbids.
	// See docs/v0.2-oracle-plan.md §2.7.
	Environment string `env:"APP_ENV,required"`
	AWSRegion   string `env:"AWS_REGION"`

	Secrets struct {
		// Interpreted by the environment's provider: a variable name locally, an SSM parameter
		// path in staging and production.
		SlasherKeyRef string `env:"SLASHER_KEY_REF"`
	}

	DB struct {
		DSN             string        `env:"DB_DSN,required"`
		MaxOpenConns    int32         `env:"DB_MAX_OPEN_CONNS" envDefault:"25"`
		MinConns        int32         `env:"DB_MIN_CONNS" envDefault:"5"`
		ConnMaxLifetime time.Duration `env:"DB_CONN_MAX_LIFETIME" envDefault:"5m"`
		ConnMaxIdleTime time.Duration `env:"DB_CONN_MAX_IDLE_TIME" envDefault:"1m"`
	}

	Redis struct {
		Addr     string `env:"REDIS_ADDR,required"`
		Password string `env:"REDIS_PASSWORD"`
		DB       int    `env:"REDIS_DB" envDefault:"0"`
	}

	Chain struct {
		RPCURL        string        `env:"CHAIN_RPC_URL"`
		ChainID       int64         `env:"CHAIN_ID"`
		StartBlock    uint64        `env:"CHAIN_START_BLOCK" envDefault:"0"`
		ConfirmBlocks uint64        `env:"CHAIN_CONFIRM_BLOCKS" envDefault:"12"`
		BatchSize     uint64        `env:"CHAIN_BATCH_SIZE" envDefault:"2000"`
		PollInterval  time.Duration `env:"CHAIN_POLL_INTERVAL" envDefault:"2s"`
	}

	Contracts struct {
		VaultEngine   string `env:"CONTRACT_VAULT_ENGINE"`
		OracleRounds  string `env:"CONTRACT_ORACLE_ROUNDS"`
		OracleStaking string `env:"CONTRACT_ORACLE_STAKING"`
		OracleStake   string `env:"CONTRACT_ORACLE_STAKE_TOKEN"`
	}

	API struct {
		Addr            string        `env:"API_ADDR" envDefault:":8090"`
		ReadTimeout     time.Duration `env:"API_READ_TIMEOUT" envDefault:"10s"`
		WriteTimeout    time.Duration `env:"API_WRITE_TIMEOUT" envDefault:"30s"`
		ShutdownTimeout time.Duration `env:"API_SHUTDOWN_TIMEOUT" envDefault:"15s"`
		MaxPageSize     int           `env:"API_MAX_PAGE_SIZE" envDefault:"100"`
	}
}

// Load reads config from the environment and fails fast if it is unusable.
func Load() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

// ValidateAggregator checks the fields the oracle aggregation service needs. The key itself is
// resolved at startup by internal/secrets, which fails rather than falling back.
func (c *Config) ValidateAggregator() error {
	if c.Secrets.SlasherKeyRef == "" {
		return fmt.Errorf("SLASHER_KEY_REF is required: it names the environment variable locally, or the SSM parameter path otherwise")
	}
	if c.Contracts.OracleStaking == "" {
		return fmt.Errorf("CONTRACT_ORACLE_STAKING is required for the aggregator")
	}
	return nil
}

// ValidateIndexer checks the fields the indexer needs beyond the shared set.
func (c *Config) ValidateIndexer() error {
	if c.Chain.RPCURL == "" {
		return fmt.Errorf("CHAIN_RPC_URL is required for the indexer")
	}
	if c.Chain.ChainID <= 0 {
		return fmt.Errorf("CHAIN_ID must be a positive chain identifier, got %d", c.Chain.ChainID)
	}
	if c.Chain.BatchSize == 0 {
		return fmt.Errorf("CHAIN_BATCH_SIZE must be greater than zero")
	}
	if c.Contracts.VaultEngine == "" {
		return fmt.Errorf("CONTRACT_VAULT_ENGINE is required for the indexer")
	}
	// The oracle contracts are optional: an operator running only v0.1 has none deployed. But the
	// staking registry records stake amounts, which are meaningless without the token that gives
	// them a scale, so those two are required together or not at all.
	if (c.Contracts.OracleStaking == "") != (c.Contracts.OracleStake == "") {
		return fmt.Errorf("CONTRACT_ORACLE_STAKING and CONTRACT_ORACLE_STAKE_TOKEN must be set together")
	}
	return nil
}

// OracleEnabled reports whether oracle contracts were configured for indexing.
func (c *Config) OracleEnabled() bool {
	return c.Contracts.OracleRounds != "" || c.Contracts.OracleStaking != ""
}
