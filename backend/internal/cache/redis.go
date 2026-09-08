// Package cache is the Redis access layer. Redis is a hot-path cache only; Postgres is the source
// of truth. Every key is namespaced and TTL'd — no permanent entries.
package cache

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/aizen299/aegis-protocol/backend/pkg/config"
)

// ErrMiss is returned when a key is absent. Callers fall back to Postgres.
var ErrMiss = errors.New("cache miss")

type Client struct {
	rdb *redis.Client
}

func New(ctx context.Context, cfg *config.Config) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

func (c *Client) Close() error { return c.rdb.Close() }

func (c *Client) Ping(ctx context.Context) error { return c.rdb.Ping(ctx).Err() }

// ChainKey builds pb:{module}:{chain_id}:{entity}:{id} for chain-derived data. An address or round
// ID is only meaningful alongside its chain, so the chain ID is part of the key.
func ChainKey(module string, chainID int64, entity, id string) string {
	base := "pb:" + module + ":" + strconv.FormatInt(chainID, 10) + ":" + entity
	if id == "" {
		return base
	}
	return base + ":" + id
}

// Key builds pb:{module}:{entity}:{id} for purely off-chain data.
func Key(module, entity, id string) string {
	base := "pb:" + module + ":" + entity
	if id == "" {
		return base
	}
	return base + ":" + id
}

func (c *Client) GetBytes(ctx context.Context, key string) ([]byte, error) {
	b, err := c.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrMiss
	}
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	return b, nil
}

// SetBytes writes with a mandatory TTL. A zero or negative ttl is a programming error.
func (c *Client) SetBytes(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("refusing to write %s without a ttl", key)
	}
	if err := c.rdb.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("set %s: %w", key, err)
	}
	return nil
}

// AcquireFill takes a short-lived lock so that only one caller repopulates a cold key. Callers that
// fail to acquire it still read through to Postgres — the lock limits stampede, it does not gate
// correctness.
func (c *Client) AcquireFill(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	ok, err := c.rdb.SetNX(ctx, key+":fill", 1, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("setnx %s: %w", key, err)
	}
	return ok, nil
}

func (c *Client) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("del: %w", err)
	}
	return nil
}
