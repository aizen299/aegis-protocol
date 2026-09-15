// Package indexer consumes chain events and is the only writer of chain-derived state.
//
// It is cursor-based (the cursor is persisted per chain), idempotent (handlers upsert), and
// reorg-safe (nothing above head - ConfirmationDepth is processed).
package indexer

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	"github.com/aizen299/aegis-protocol/backend/internal/observability"
)

// CursorStore persists indexing progress. Narrowed to an interface so the indexer's
// reorg-safety and restart logic is testable without a database.
type CursorStore interface {
	LoadCursor(ctx context.Context, service string, chainID int64) (uint64, bool, error)
	SaveCursor(ctx context.Context, service string, chainID int64, block uint64) error
}

// Handler processes one decoded event. Implementations must be idempotent: the same event may be
// delivered more than once after a restart or a backfill.
type Handler interface {
	Filters() []chain.Filter
	Handle(ctx context.Context, ev chain.Event) error
}

type Options struct {
	ServiceName  string
	StartBlock   uint64
	BatchSize    uint64
	PollInterval time.Duration
	RetryBackoff time.Duration

	// Metrics is optional. Nil disables emission, which is what the tests and local runs use.
	Metrics *observability.Metrics
}

type Indexer struct {
	client   chain.Client
	store    CursorStore
	handlers []Handler
	filters  []chain.Filter
	log      zerolog.Logger
	opts     Options
	cursor   uint64
}

func New(client chain.Client, store CursorStore, log zerolog.Logger, opts Options, handlers ...Handler) *Indexer {
	if opts.RetryBackoff == 0 {
		opts.RetryBackoff = 5 * time.Second
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = 2 * time.Second
	}

	var filters []chain.Filter
	for _, h := range handlers {
		filters = append(filters, h.Filters()...)
	}

	return &Indexer{
		client:   client,
		store:    store,
		handlers: handlers,
		filters:  filters,
		log:      log.With().Int64("chain_id", client.ChainID()).Logger(),
		opts:     opts,
	}
}

// Cursor returns the last block committed to Postgres.
func (idx *Indexer) Cursor() uint64 { return idx.cursor }

// Restore loads the persisted cursor. Run calls it; a caller driving Step directly must call it
// first.
func (idx *Indexer) Restore(ctx context.Context) error { return idx.restoreCursor(ctx) }

// Run resumes from the persisted cursor and blocks until ctx is cancelled.
func (idx *Indexer) Run(ctx context.Context) error {
	if err := idx.restoreCursor(ctx); err != nil {
		return err
	}

	idx.log.Info().Uint64("cursor", idx.cursor).Uint64("confirm_blocks", idx.client.ConfirmationDepth()).
		Msg("indexer started")

	for {
		if err := sleepCtx(ctx, 0); err != nil {
			return nil
		}

		advanced, err := idx.Step(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			idx.log.Error().Err(err).Uint64("cursor", idx.cursor).Msg("indexer step failed")
			if err := sleepCtx(ctx, idx.opts.RetryBackoff); err != nil {
				return nil
			}
			continue
		}

		if !advanced {
			if err := sleepCtx(ctx, idx.opts.PollInterval); err != nil {
				return nil
			}
		}
	}
}

// Step processes at most one batch and reports whether the cursor moved. Run drives this in a
// loop; it is exported so a caller can advance the indexer deterministically instead of waiting on
// wall-clock polling.
func (idx *Indexer) Step(ctx context.Context) (bool, error) {
	head, err := idx.client.Head(ctx)
	if err != nil {
		return false, fmt.Errorf("head: %w", err)
	}

	confirms := idx.client.ConfirmationDepth()
	if head < confirms {
		return false, nil
	}

	safeHead := head - confirms
	if safeHead <= idx.cursor {
		return false, nil
	}

	from := idx.cursor + 1
	to := min(idx.cursor+idx.opts.BatchSize, safeHead)

	events, err := idx.client.LogsInRange(ctx, from, to, idx.filters)
	if err != nil {
		return false, fmt.Errorf("logs [%d,%d]: %w", from, to, err)
	}

	for _, ev := range events {
		if err := idx.dispatch(ctx, ev); err != nil {
			return false, err
		}
	}

	// The cursor is committed only after every event in the batch is durable. A crash before this
	// point replays the batch, which the idempotent handlers absorb.
	if err := idx.store.SaveCursor(ctx, idx.opts.ServiceName, idx.client.ChainID(), to); err != nil {
		return false, err
	}
	idx.cursor = to

	if len(events) > 0 {
		idx.log.Info().Uint64("from", from).Uint64("to", to).Int("events", len(events)).
			Uint64("lag", head-to).Msg("batch processed")
	}

	idx.emitLag(ctx, head, to)
	return true, nil
}

// emitLag publishes how far behind the chain the committed data is.
//
// Seconds, not blocks. docs/devops.md asks for an alarm at 100 blocks, which on Arbitrum is about
// 25 seconds — inside a single batch of the indexer's own window, so it would fire constantly and
// be muted. The block count is emitted too because it is what an operator debugging a stalled
// cursor wants to see; the alarm is on the seconds. See docs/v1.0-production-plan.md §2.2.
//
// A failure here is logged at debug and dropped: an indexer that stops indexing because it could
// not report its lag has turned observability into an outage.
func (idx *Indexer) emitLag(ctx context.Context, head, processed uint64) {
	if idx.opts.Metrics == nil {
		return
	}

	blocks := uint64(0)
	if head > processed {
		blocks = head - processed
	}

	processedAt, err := idx.client.BlockTime(ctx, processed)
	if err != nil {
		idx.log.Debug().Err(err).Uint64("block", processed).Msg("lag in seconds unavailable")
		idx.opts.Metrics.Emit(observability.Measurement{
			Name:  observability.MetricIndexerLagBlocks,
			Value: float64(blocks),
			Unit:  observability.UnitCount,
		})
		return
	}

	// Measured against the wall clock rather than the head block's timestamp: what an operator
	// needs to know is how stale the served data is, and a chain that stops producing blocks
	// entirely would otherwise report a lag of zero.
	seconds := time.Now().Unix() - processedAt
	if seconds < 0 {
		seconds = 0
	}

	idx.opts.Metrics.Emit(
		observability.Measurement{
			Name:  observability.MetricIndexerLagSeconds,
			Value: float64(seconds),
			Unit:  observability.UnitSeconds,
		},
		observability.Measurement{
			Name:  observability.MetricIndexerLagBlocks,
			Value: float64(blocks),
			Unit:  observability.UnitCount,
		},
	)
}

func (idx *Indexer) dispatch(ctx context.Context, ev chain.Event) error {
	for _, h := range idx.handlers {
		if err := h.Handle(ctx, ev); err != nil {
			return fmt.Errorf("handle %s at block %d: %w", ev.Name, ev.BlockNumber, err)
		}
	}
	return nil
}

func (idx *Indexer) restoreCursor(ctx context.Context) error {
	last, found, err := idx.store.LoadCursor(ctx, idx.opts.ServiceName, idx.client.ChainID())
	if err != nil {
		return err
	}

	if !found {
		idx.cursor = idx.opts.StartBlock
		return idx.store.SaveCursor(ctx, idx.opts.ServiceName, idx.client.ChainID(), idx.cursor)
	}

	idx.cursor = last
	if idx.opts.StartBlock > last {
		idx.log.Warn().Uint64("cursor", last).Uint64("start_block", idx.opts.StartBlock).
			Msg("configured start block is ahead of the persisted cursor; resuming from the cursor")
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d == 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
