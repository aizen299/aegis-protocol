package oraclenode

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/aizen299/aegis-protocol/backend/internal/oracle"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// Fetcher reduces several independent sources to one price.
//
// The median is taken across sources before anything is signed, so a single compromised or broken
// source cannot move what this node reports. That matters more here than on chain: the contract's
// median protects the feed from a bad node, and this one protects the node from a bad source.
type Fetcher struct {
	sources    []Source
	minSources int
	maxRetries int
	baseDelay  time.Duration
	log        zerolog.Logger
}

type FetcherOptions struct {
	// MinSources is the number that must answer for a round to be worth submitting to. Below it the
	// node stays silent: a missed round costs 0.5% of stake, a wrong submission costs 1% and
	// pollutes the median everyone else relies on.
	MinSources int
	MaxRetries int
	BaseDelay  time.Duration
}

func NewFetcher(sources []Source, log zerolog.Logger, opts FetcherOptions) *Fetcher {
	if opts.MinSources == 0 {
		opts.MinSources = 3
	}
	if opts.MaxRetries == 0 {
		opts.MaxRetries = 5
	}
	if opts.BaseDelay == 0 {
		opts.BaseDelay = 200 * time.Millisecond
	}
	return &Fetcher{
		sources:    sources,
		minSources: opts.MinSources,
		maxRetries: opts.MaxRetries,
		baseDelay:  opts.BaseDelay,
		log:        log,
	}
}

// Price fetches from every source concurrently and returns the median.
//
// Returns ErrTooFewSources rather than a value when too few answered. The caller is expected to
// skip the round: reporting a price derived from one source would be presenting a guess with the
// same confidence as a real reading.
func (f *Fetcher) Price(ctx context.Context, decimals uint8) (types.Raw, FetchResult, error) {
	result := FetchResult{Failed: map[string]error{}}

	var (
		mu    sync.Mutex
		group errgroup.Group
	)

	for _, source := range f.sources {
		group.Go(func() error {
			value, err := f.fetchWithBackoff(ctx, source, decimals)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				// One source failing is expected and not fatal; the count check below decides
				// whether enough of them answered.
				result.Failed[source.Name()] = err
				return nil
			}
			result.Values = append(result.Values, value)
			result.Sources = append(result.Sources, source.Name())
			return nil
		})
	}
	_ = group.Wait()

	if len(result.Values) < f.minSources {
		return types.Raw{}, result, fmt.Errorf("%w: %d of %d answered, need %d",
			ErrTooFewSources, len(result.Values), len(f.sources), f.minSources)
	}

	return oracle.Median(result.Values), result, nil
}

// fetchWithBackoff retries one source with exponential delay.
//
// Bounded because a round has a deadline: retrying past it converts a recoverable failure into a
// missed round for every source, not just the slow one.
func (f *Fetcher) fetchWithBackoff(ctx context.Context, source Source, decimals uint8) (types.Raw, error) {
	var lastErr error

	for attempt := 0; attempt < f.maxRetries; attempt++ {
		if attempt > 0 {
			delay := f.baseDelay << (attempt - 1)
			select {
			case <-ctx.Done():
				return types.Raw{}, ctx.Err()
			case <-time.After(delay):
			}
		}

		value, err := source.Fetch(ctx, decimals)
		if err == nil {
			if value.IsZero() {
				lastErr = fmt.Errorf("source reported zero")
				continue
			}
			return value, nil
		}
		lastErr = err

		if ctx.Err() != nil {
			return types.Raw{}, ctx.Err()
		}
	}

	return types.Raw{}, fmt.Errorf("%s: %w", source.Name(), lastErr)
}
