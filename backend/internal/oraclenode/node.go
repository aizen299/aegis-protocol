package oraclenode

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// RoundView is what the node needs to know about the round it might submit to.
type RoundView struct {
	RoundID   types.Raw
	FeedID    string
	Decimals  uint8
	Deadline  time.Time
	Open      bool
	Eligible  bool
	Submitted bool
}

// Chain is the node's view of the contracts. Narrow so the submission loop can be tested without a
// chain, and so a non-EVM implementation has a small surface to satisfy.
type Chain interface {
	CurrentRound(ctx context.Context, feedID string) (RoundView, error)
	Submit(ctx context.Context, roundID types.Raw, feedID string, value types.Raw) (txHash string, err error)
}

type NodeOptions struct {
	FeedID       string
	PollInterval time.Duration
	// SubmitBefore leaves headroom ahead of the deadline. Submitting into the last moments risks
	// the transaction confirming after the round closed, which reads as a missed round.
	SubmitBefore time.Duration
}

// Node submits one price per round for one feed.
type Node struct {
	chain   Chain
	fetcher *Fetcher
	log     zerolog.Logger
	opts    NodeOptions
}

func NewNode(chain Chain, fetcher *Fetcher, log zerolog.Logger, opts NodeOptions) *Node {
	if opts.PollInterval == 0 {
		opts.PollInterval = 5 * time.Second
	}
	if opts.SubmitBefore == 0 {
		opts.SubmitBefore = 15 * time.Second
	}
	return &Node{chain: chain, fetcher: fetcher, log: log, opts: opts}
}

// Step evaluates the current round once and submits if it should. Reports whether it submitted.
func (n *Node) Step(ctx context.Context) (bool, error) {
	round, err := n.chain.CurrentRound(ctx, n.opts.FeedID)
	if err != nil {
		return false, fmt.Errorf("read current round: %w", err)
	}

	switch {
	case !round.Open:
		return false, nil
	case round.Submitted:
		// One submission per round is enforced on chain; not attempting a second saves the gas a
		// revert would cost.
		return false, nil
	case !round.Eligible:
		// Not in the set frozen when this round opened. Submitting would revert.
		n.log.Debug().Str("round", round.RoundID.String()).Msg("not eligible for this round")
		return false, nil
	}

	remaining := time.Until(round.Deadline)
	if remaining <= n.opts.SubmitBefore {
		n.log.Warn().
			Str("round", round.RoundID.String()).
			Dur("remaining", remaining).
			Msg("too close to the deadline to submit safely; skipping")
		return false, nil
	}

	// Fetching is bounded by the headroom, so a slow source cannot push the submission past the
	// deadline it was trying to beat.
	fetchCtx, cancel := context.WithTimeout(ctx, remaining-n.opts.SubmitBefore)
	defer cancel()

	value, result, err := n.fetcher.Price(fetchCtx, round.Decimals)
	if err != nil {
		if errors.Is(err, ErrTooFewSources) {
			// Deliberate silence. A missed round costs 0.5% of stake; a submission derived from too
			// few sources costs 1% and misleads every consumer of the feed.
			n.log.Warn().
				Str("round", round.RoundID.String()).
				Int("answered", len(result.Values)).
				Interface("failed", failureNames(result)).
				Msg("skipping round: not enough sources answered")
			return false, nil
		}
		return false, fmt.Errorf("fetch price: %w", err)
	}

	txHash, err := n.chain.Submit(ctx, round.RoundID, round.FeedID, value)
	if err != nil {
		return false, fmt.Errorf("submit: %w", err)
	}

	n.log.Info().
		Str("round", round.RoundID.String()).
		Str("price", FormatScaled(value, round.Decimals)).
		Strs("sources", result.Sources).
		Str("tx", txHash).
		Msg("submitted")

	return true, nil
}

// Run polls until the context is cancelled.
func (n *Node) Run(ctx context.Context) error {
	ticker := time.NewTicker(n.opts.PollInterval)
	defer ticker.Stop()

	for {
		if _, err := n.Step(ctx); err != nil && ctx.Err() == nil {
			// A failed round is recoverable — the next one is another chance — so this logs and
			// continues rather than taking the process down.
			n.log.Error().Err(err).Msg("round step failed")
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func failureNames(result FetchResult) map[string]string {
	out := make(map[string]string, len(result.Failed))
	for name, err := range result.Failed {
		out[name] = err.Error()
	}
	return out
}
