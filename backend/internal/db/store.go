// Package db is the PostgreSQL access layer. pgx/v5 directly, no ORM. Every query is a
// parameterised constant declared next to the method that runs it.
package db

import (
	"context"
	"fmt"

	pgxdecimal "github.com/jackc/pgx-shopspring-decimal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aizen299/aegis-protocol/backend/pkg/config"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, cfg *config.Config) (*Store, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DB.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}

	poolCfg.MaxConns = cfg.DB.MaxOpenConns
	poolCfg.MinConns = cfg.DB.MinConns
	poolCfg.MaxConnLifetime = cfg.DB.ConnMaxLifetime
	poolCfg.MaxConnIdleTime = cfg.DB.ConnMaxIdleTime

	// NUMERIC(38,18) columns scan into shopspring decimals. Never float.
	poolCfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		pgxdecimal.Register(conn.TypeMap())
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// QueryRowForTest runs an arbitrary query against the pool.
//
// Only for the end-to-end suite, which asserts on rows the typed accessors deliberately do not
// expose. Production code uses the parameterised query constants declared beside each method.
func (s *Store) QueryRowForTest(ctx context.Context, sql string, args ...any) pgx.Row {
	return s.pool.QueryRow(ctx, sql, args...)
}
