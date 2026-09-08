package config

import (
	"strings"
	"testing"
)

// APP_ENV has no default on purpose. A default would mean an unconfigured process decides it is in
// development, which is the inference the secret policy exists to forbid.
func TestEnvironmentIsRequired(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://localhost/test")
	t.Setenv("REDIS_ADDR", "localhost:6379")

	if _, err := Load(); err == nil {
		t.Fatal("config loaded without APP_ENV; the environment must be declared, not defaulted")
	} else if !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("err = %v, should name the missing variable", err)
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
	cfg.Contracts.OracleStaking = "0x0000000000000000000000000000000000000001"

	if err := cfg.ValidateAggregator(); err == nil {
		t.Fatal("aggregator validated without SLASHER_KEY_REF")
	}

	cfg.Secrets.SlasherKeyRef = "/aegis/staging/slasher_key"
	if err := cfg.ValidateAggregator(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}
