// Package zk serves read models over indexed zk state.
package zk

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/cache"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	gateTTL     = 300 * time.Second
	fillLockTTL = 5 * time.Second
)

// Reader is the storage surface the service reads through.
type Reader interface {
	ZkGate(ctx context.Context, chainID int64) (types.ZkGateMetadata, error)
	AnonymitySet(ctx context.Context, chainID int64, tree string) (types.AnonymitySet, error)
	ListCommitments(ctx context.Context, chainID int64, tree string, limit, offset int) ([]types.Commitment, error)
	ListPrivateActions(ctx context.Context, chainID int64, gate string, limit, offset int) ([]types.PrivateAction, error)
	NullifierSpent(ctx context.Context, chainID int64, gate, nullifier string) (bool, error)
	ListZkActions(ctx context.Context, chainID int64, gate string, limit, offset int) ([]types.ZkAction, error)
}

// Cache is the caching surface. Optional: a nil cache degrades to reading through.
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

// Nothing but the gate's wiring is cached.
//
// A commitment list feeds a Merkle path, and a path built from a stale list produces a proof that
// verifies against no root the contract holds. A spent-nullifier answer decides whether a client
// pays to generate a proof at all. Both are worth reading fresh.

func (s *Service) Gate(ctx context.Context) (types.ZkGateMetadata, error) {
	key := cache.ChainKey("zk", s.chainID, "gate", "")

	if cached, ok := s.readCached(ctx, key); ok {
		var g types.ZkGateMetadata
		if json.Unmarshal(cached, &g) == nil {
			return g, nil
		}
	}

	gate, err := s.store.ZkGate(ctx, s.chainID)
	if err != nil {
		return gate, err
	}
	s.fill(ctx, key, gate, gateTTL)
	return gate, nil
}

// AnonymitySet is what bounds the privacy of any proof built against this tree. It is served
// wherever a proof is offered rather than left for a caller to infer — docs/v0.4-zk-plan.md §4.
func (s *Service) AnonymitySet(ctx context.Context, tree string) (types.AnonymitySet, error) {
	return s.store.AnonymitySet(ctx, s.chainID, tree)
}

func (s *Service) Commitments(ctx context.Context, tree string, limit, offset int) ([]types.Commitment, error) {
	return s.store.ListCommitments(ctx, s.chainID, tree, limit, offset)
}

func (s *Service) PrivateActions(ctx context.Context, gate string, limit, offset int) ([]types.PrivateAction, error) {
	return s.store.ListPrivateActions(ctx, s.chainID, gate, limit, offset)
}

func (s *Service) NullifierSpent(ctx context.Context, gate, nullifier string) (bool, error) {
	return s.store.NullifierSpent(ctx, s.chainID, gate, nullifier)
}

func (s *Service) Actions(ctx context.Context, gate string, limit, offset int) ([]types.ZkAction, error) {
	return s.store.ListZkActions(ctx, s.chainID, gate, limit, offset)
}

func (s *Service) readCached(ctx context.Context, key string) ([]byte, bool) {
	if s.cache == nil {
		return nil, false
	}
	value, err := s.cache.GetBytes(ctx, key)
	if err != nil || len(value) == 0 {
		return nil, false
	}
	return value, true
}

// fill is best-effort: a cache that refuses a write must never fail a read that already succeeded.
func (s *Service) fill(ctx context.Context, key string, value any, ttl time.Duration) {
	if s.cache == nil {
		return
	}
	ok, err := s.cache.AcquireFill(ctx, key, fillLockTTL)
	if err != nil || !ok {
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	if err := s.cache.SetBytes(ctx, key, encoded, ttl); err != nil {
		s.log.Debug().Err(err).Str("key", key).Msg("zk cache fill failed")
	}
}
