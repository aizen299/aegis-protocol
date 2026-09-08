// Package vault serves read models derived from indexed vault events.
package vault

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/cache"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	positionTTL = 60 * time.Second
	tvlTTL      = 30 * time.Second
	fillLockTTL = 5 * time.Second
)

type Service struct {
	store   *db.Store
	cache   *cache.Client
	log     zerolog.Logger
	chainID int64
}

func NewService(store *db.Store, c *cache.Client, log zerolog.Logger, chainID int64) *Service {
	return &Service{store: store, cache: c, log: log, chainID: chainID}
}

// Position reads through Redis to Postgres. A cache failure degrades to a database read rather than
// failing the request.
func (s *Service) Position(ctx context.Context, user string) (types.VaultPosition, error) {
	key := cache.ChainKey("vault", s.chainID, "position", user)

	if raw, err := s.cache.GetBytes(ctx, key); err == nil {
		var pos types.VaultPosition
		if json.Unmarshal(raw, &pos) == nil {
			return pos, nil
		}
	} else if !errors.Is(err, cache.ErrMiss) {
		s.log.Warn().Err(err).Str("key", key).Msg("cache read failed")
	}

	pos, err := s.store.VaultPosition(ctx, s.chainID, user)
	if err != nil {
		return pos, err
	}

	s.fill(ctx, key, pos, positionTTL)
	return pos, nil
}

// TVL reports the vault's total value in raw base units alongside the asset's decimals. Scaling
// happens at the presentation edge, never here.
type TVL struct {
	Vault    string    `json:"vault"`
	Asset    string    `json:"asset,omitempty"`
	Amount   types.Raw `json:"amount"`
	Decimals uint8     `json:"decimals"`
}

func (s *Service) TVL(ctx context.Context, vaultAddress string) (TVL, error) {
	key := cache.ChainKey("vault", s.chainID, "tvl", vaultAddress)

	if raw, err := s.cache.GetBytes(ctx, key); err == nil {
		var cached TVL
		if json.Unmarshal(raw, &cached) == nil {
			return cached, nil
		}
	} else if !errors.Is(err, cache.ErrMiss) {
		s.log.Warn().Err(err).Str("key", key).Msg("cache read failed")
	}

	amount, decimals, asset, err := s.store.VaultTVL(ctx, s.chainID, vaultAddress)
	if err != nil {
		return TVL{}, err
	}

	tvl := TVL{Vault: vaultAddress, Asset: asset, Amount: amount, Decimals: decimals}
	s.fill(ctx, key, tvl, tvlTTL)
	return tvl, nil
}

func (s *Service) Deposits(ctx context.Context, user string, limit, offset int) ([]types.VaultDeposit, error) {
	return s.store.ListVaultDeposits(ctx, s.chainID, user, limit, offset)
}

func (s *Service) fill(ctx context.Context, key string, value any, ttl time.Duration) {
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
