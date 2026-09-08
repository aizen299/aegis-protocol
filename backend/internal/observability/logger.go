// Package observability wires structured logging. zerolog throughout — one logger, propagated.
package observability

import (
	"os"
	"time"

	"github.com/rs/zerolog"
)

// NewLogger builds the process logger. An unparseable level falls back to info rather than failing
// startup, since a bad log level should not take a service down.
func NewLogger(service, level string) zerolog.Logger {
	parsed, err := zerolog.ParseLevel(level)
	if err != nil || parsed == zerolog.NoLevel {
		parsed = zerolog.InfoLevel
	}

	zerolog.TimeFieldFormat = time.RFC3339Nano
	return zerolog.New(os.Stdout).
		Level(parsed).
		With().
		Timestamp().
		Str("service", service).
		Logger()
}
