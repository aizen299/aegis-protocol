// Package oracle serves read models over indexed oracle state.
package oracle

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/cache"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	feedTTL     = 60 * time.Second
	nodeTTL     = 120 * time.Second
	fillLockTTL = 5 * time.Second
)

// Reader is the storage surface the service reads through. Narrow so handlers can be tested
// without a database.
type Reader interface {
	ListOracleFeeds(ctx context.Context, chainID int64, limit, offset int) ([]types.OracleFeed, error)
	OracleFeed(ctx context.Context, chainID int64, feedID string) (types.OracleFeed, error)
	ListOracleRounds(ctx context.Context, chainID int64, feedID string, limit, offset int) ([]types.OracleRound, error)
	OracleRound(ctx context.Context, chainID int64, roundID types.Raw) (types.OracleRound, error)
	ListOracleSubmissions(ctx context.Context, chainID int64, roundID types.Raw, limit, offset int) ([]types.OracleSubmission, error)
	ListOracleNodes(ctx context.Context, chainID int64, limit, offset int) ([]types.OracleNode, error)
	OracleNode(ctx context.Context, chainID int64, address string) (types.OracleNode, error)
}

// Cache is the caching surface. Optional at construction: a nil cache degrades to reading through.
type Cache interface {
	GetBytes(ctx context.Context, key string) ([]byte, error)
	SetBytes(ctx context.Context, key string, value []byte, ttl time.Duration) error
	AcquireFill(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

type Service struct {
	store   Reader
	cache   Cache
	log     zerolog.Logger
	chainID int64
}

func NewService(store Reader, c Cache, log zerolog.Logger, chainID int64) *Service {
	return &Service{store: store, cache: c, log: log, chainID: chainID}
}

// Rounds and submissions are deliberately not cached. They are the audit trail an operator reaches
// for when a settlement looks wrong, and a stale answer there is worse than a slow one. Feeds and
// the node registry change rarely and are read constantly, so those are cached.

func (s *Service) Feeds(ctx context.Context, limit, offset int) ([]types.OracleFeed, error) {
	if offset == 0 {
		if cached, ok := s.readCached(ctx, cache.ChainKey("oracle", s.chainID, "feeds", "")); ok {
			var feeds []types.OracleFeed
			if json.Unmarshal(cached, &feeds) == nil {
				return feeds, nil
			}
		}
	}

	feeds, err := s.store.ListOracleFeeds(ctx, s.chainID, limit, offset)
	if err != nil {
		return nil, err
	}
	if offset == 0 {
		s.fill(ctx, cache.ChainKey("oracle", s.chainID, "feeds", ""), feeds, feedTTL)
	}
	return feeds, nil
}

func (s *Service) Feed(ctx context.Context, feedID string) (types.OracleFeed, error) {
	return s.store.OracleFeed(ctx, s.chainID, feedID)
}

func (s *Service) Rounds(ctx context.Context, feedID string, limit, offset int) ([]types.OracleRound, error) {
	return s.store.ListOracleRounds(ctx, s.chainID, feedID, limit, offset)
}

func (s *Service) Round(ctx context.Context, roundID types.Raw) (types.OracleRound, error) {
	return s.store.OracleRound(ctx, s.chainID, roundID)
}

func (s *Service) Submissions(ctx context.Context, roundID types.Raw, limit, offset int) ([]types.OracleSubmission, error) {
	return s.store.ListOracleSubmissions(ctx, s.chainID, roundID, limit, offset)
}

func (s *Service) Nodes(ctx context.Context, limit, offset int) ([]types.OracleNode, error) {
	if offset == 0 {
		if cached, ok := s.readCached(ctx, cache.ChainKey("oracle", s.chainID, "nodes", "")); ok {
			var nodes []types.OracleNode
			if json.Unmarshal(cached, &nodes) == nil {
				return nodes, nil
			}
		}
	}

	nodes, err := s.store.ListOracleNodes(ctx, s.chainID, limit, offset)
	if err != nil {
		return nil, err
	}
	if offset == 0 {
		s.fill(ctx, cache.ChainKey("oracle", s.chainID, "nodes", ""), nodes, nodeTTL)
	}
	return nodes, nil
}

func (s *Service) Node(ctx context.Context, address string) (types.OracleNode, error) {
	return s.store.OracleNode(ctx, s.chainID, address)
}

func (s *Service) readCached(ctx context.Context, key string) ([]byte, bool) {
	if s.cache == nil {
		return nil, false
	}
	raw, err := s.cache.GetBytes(ctx, key)
	if err != nil {
		if !errors.Is(err, cache.ErrMiss) {
			s.log.Warn().Err(err).Str("key", key).Msg("cache read failed")
		}
		return nil, false
	}
	return raw, true
}

func (s *Service) fill(ctx context.Context, key string, value any, ttl time.Duration) {
	if s.cache == nil {
		return
	}
	ok, err := s.cache.AcquireFill(ctx, key, fillLockTTL)
	if err != nil || !ok {
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		s.log.Warn().Err(err).Str("key", key).Msg("cache encode failed")
		return
	}
	if err := s.cache.SetBytes(ctx, key, raw, ttl); err != nil {
		s.log.Warn().Err(err).Str("key", key).Msg("cache write failed")
	}
}
