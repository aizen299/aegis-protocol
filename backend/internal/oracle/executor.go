package oracle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// PendingSlash is a decision the aggregator recorded that has not reached the chain.
type PendingSlash struct {
	ID      string
	ChainID int64
	Node    string
	RoundID types.Raw
	Amount  types.Raw
	Reason  string
	// Attempts counts submissions already made for this decision. A decision that keeps failing is
	// an operational problem, not something to retry forever.
	Attempts int
}

// ExecutorStore is the surface the executor needs.
type ExecutorStore interface {
	ClaimPendingSlashes(ctx context.Context, chainID int64, limit, maxAttempts int) ([]PendingSlash, error)
	MarkSlashSubmitted(ctx context.Context, id, txHash string, at time.Time) error
	MarkSlashAbandoned(ctx context.Context, id, reason string, at time.Time) error
}

// Slasher submits a penalty to a chain.
//
// Narrow on purpose: the executor holds a key that moves value, and the smaller the surface it can
// reach, the less a bug in it can do.
type Slasher interface {
	Slash(ctx context.Context, node string, roundID, amount types.Raw, reason string) (txHash string, err error)
}

// ErrAlreadySlashed is returned when the chain rejects a penalty the node has already taken for
// that round. Not a failure: it is the guard doing its job on a retry.
var ErrAlreadySlashed = errors.New("node already slashed for this round")

type ExecutorOptions struct {
	ChainID     int64
	BatchSize   int
	MaxAttempts int
}

// Executor submits recorded slash decisions to the chain.
//
// Separate from the Aggregator by design. Deciding and executing have different failure modes and
// different privileges: the aggregator holds no key and cannot move anything, and this holds a key
// but makes no judgements. A crash between the two loses nothing, because the decision is already
// durable before this runs.
type Executor struct {
	store   ExecutorStore
	slasher Slasher
	log     zerolog.Logger
	opts    ExecutorOptions
}

func NewExecutor(store ExecutorStore, slasher Slasher, log zerolog.Logger, opts ExecutorOptions) *Executor {
	if opts.BatchSize == 0 {
		opts.BatchSize = 10
	}
	if opts.MaxAttempts == 0 {
		opts.MaxAttempts = 3
	}
	return &Executor{store: store, slasher: slasher, log: log, opts: opts}
}

// Step submits one batch and reports how many decisions it handled.
//
// A failure on one decision does not abandon the batch: an unrelated node's penalty should not be
// held up by another's. Failures are counted and a decision that exhausts its attempts is
// abandoned rather than retried forever, which turns a stuck penalty into something an operator can
// see instead of a loop.
func (e *Executor) Step(ctx context.Context) (int, error) {
	pending, err := e.store.ClaimPendingSlashes(ctx, e.opts.ChainID, e.opts.BatchSize, e.opts.MaxAttempts)
	if err != nil {
		return 0, fmt.Errorf("claim pending slashes: %w", err)
	}

	var handled int
	for _, slash := range pending {
		if err := e.submit(ctx, slash); err != nil {
			e.log.Error().Err(err).
				Str("node", slash.Node).
				Str("round", slash.RoundID.String()).
				Int("attempts", slash.Attempts+1).
				Msg("slash submission failed")
			continue
		}
		handled++
	}
	return handled, nil
}

func (e *Executor) submit(ctx context.Context, slash PendingSlash) error {
	txHash, err := e.slasher.Slash(ctx, slash.Node, slash.RoundID, slash.Amount, slash.Reason)

	switch {
	case errors.Is(err, ErrAlreadySlashed):
		// The chain says this node already took a penalty for this round. Either an earlier attempt
		// landed and we lost track of it, or someone slashed manually. Either way the decision is
		// satisfied and retrying would only fail again.
		e.log.Warn().
			Str("node", slash.Node).
			Str("round", slash.RoundID.String()).
			Msg("penalty already applied on chain; closing the decision")
		return e.store.MarkSlashAbandoned(ctx, slash.ID, "already slashed on chain", time.Now().UTC())

	case err != nil:
		if slash.Attempts+1 >= e.opts.MaxAttempts {
			e.log.Error().Err(err).
				Str("node", slash.Node).
				Str("round", slash.RoundID.String()).
				Msg("slash abandoned after repeated failures; needs an operator")
			return e.store.MarkSlashAbandoned(ctx, slash.ID, err.Error(), time.Now().UTC())
		}
		return err
	}

	e.log.Info().
		Str("node", slash.Node).
		Str("round", slash.RoundID.String()).
		Str("amount", slash.Amount.String()).
		Str("reason", slash.Reason).
		Str("tx", txHash).
		Msg("slash submitted")

	// Recorded as submitted, not executed. The indexer sets executed_at when it sees NodeSlashed,
	// so the audit trail reflects what the chain confirmed rather than what was sent — a submitted
	// transaction can still be dropped.
	return e.store.MarkSlashSubmitted(ctx, slash.ID, txHash, time.Now().UTC())
}
