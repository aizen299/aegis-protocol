// Package governance serves read models over indexed governance state.
package governance

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/cache"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	governorTTL = 300 * time.Second
	fillLockTTL = 5 * time.Second
)

// Reader is the storage surface the service reads through. Narrow so handlers can be tested
// without a database.
type Reader interface {
	ListProposals(ctx context.Context, chainID int64, state string, limit, offset int) ([]types.Proposal, error)
	Proposal(ctx context.Context, chainID int64, proposalID types.Raw) (types.Proposal, error)
	ListProposalVotes(ctx context.Context, chainID int64, proposalID types.Raw, limit, offset int) ([]types.Vote, error)
	ListVotesByVoter(ctx context.Context, chainID int64, voter string, limit, offset int) ([]types.Vote, error)
	Governor(ctx context.Context, chainID int64) (types.GovernorMetadata, error)
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

// Proposals and votes are deliberately not cached. A queued proposal's remaining delay is the
// thing an operator is watching, and a stale answer about what is about to execute is worse than a
// slow one. Only the governor's own metadata, which changes at deployment, is cached.

func (s *Service) Proposals(ctx context.Context, state string, limit, offset int) ([]types.Proposal, error) {
	return s.store.ListProposals(ctx, s.chainID, state, limit, offset)
}

func (s *Service) Proposal(ctx context.Context, proposalID types.Raw) (types.Proposal, error) {
	return s.store.Proposal(ctx, s.chainID, proposalID)
}

func (s *Service) ProposalVotes(ctx context.Context, proposalID types.Raw, limit, offset int) ([]types.Vote, error) {
	return s.store.ListProposalVotes(ctx, s.chainID, proposalID, limit, offset)
}

func (s *Service) VotesByVoter(ctx context.Context, voter string, limit, offset int) ([]types.Vote, error) {
	return s.store.ListVotesByVoter(ctx, s.chainID, voter, limit, offset)
}

func (s *Service) Governor(ctx context.Context) (types.GovernorMetadata, error) {
	key := cache.ChainKey("governance", s.chainID, "governor", "")

	if cached, ok := s.readCached(ctx, key); ok {
		var g types.GovernorMetadata
		if json.Unmarshal(cached, &g) == nil {
			return g, nil
		}
	}

	g, err := s.store.Governor(ctx, s.chainID)
	if err != nil {
		return g, err
	}
	s.fill(ctx, key, g, governorTTL)
	return g, nil
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
		s.log.Debug().Err(err).Str("key", key).Msg("governance cache fill failed")
	}
}
