package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Service string `env:"SERVICE_NAME" envDefault:"backend"`
	LogSvc  string `env:"LOG_LEVEL" envDefault:"info"`

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
		VaultEngine string `env:"CONTRACT_VAULT_ENGINE"`
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
	return nil
}
