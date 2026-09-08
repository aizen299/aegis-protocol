package oracle

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// Slash reason codes, matching the schedule in docs/oracle.md. Stored as text and passed on-chain
// as bytes32.
const (
	ReasonOutlier          = "OUTLIER_SUBMISSION"
	ReasonInvalidSignature = "INVALID_SIGNATURE"
	ReasonMissedRound      = "MISSED_ROUND"
)

// Penalties in basis points of remaining stake. All sit under the contract's 10% per-slash cap, so
// the cap constrains a compromised key without constraining intended behaviour.
const (
	penaltyOutlierBps          = 100 // 1%
	penaltyInvalidSignatureBps = 500 // 5%
	penaltyMissedRoundBps      = 50  // 0.5%
)

// AggregatorStore is the write surface the analysis needs.
type AggregatorStore interface {
	UnaggregatedRounds(ctx context.Context, chainID int64, limit int) ([]types.OracleRound, error)
	ListOracleSubmissions(ctx context.Context, chainID int64, roundID types.Raw, limit, offset int) ([]types.OracleSubmission, error)
	SubmissionSignature(ctx context.Context, chainID int64, roundID types.Raw, node string) ([]byte, error)
	RecordSubmissionVerification(ctx context.Context, chainID int64, roundID types.Raw, node string, valid bool, outlier bool) error
	RecordSlashDecision(ctx context.Context, d SlashDecision) error
	MarkRoundAggregated(ctx context.Context, chainID int64, roundID, serviceValue types.Raw, mismatch bool, at time.Time) error
	OracleNode(ctx context.Context, chainID int64, address string) (types.OracleNode, error)
}

// SignatureVerifier checks a stored signature against the submission it claims to cover.
type SignatureVerifier interface {
	Verify(chainID int64, roundID types.Raw, feedID string, value types.Raw, node string, nonce types.Raw, signature []byte) (bool, error)
}

// SlashDecision is what the service concluded, before anything is sent to a chain.
type SlashDecision struct {
	ChainID int64
	Node    string
	RoundID types.Raw
	Amount  types.Raw
	Reason  string
}

type AggregatorOptions struct {
	ChainID          int64
	RoundsContract   types.Identity
	OutlierThreshBps int64
	BatchSize        int
	MaxSubmissions   int
}

// Aggregator analyses settled rounds: it verifies signatures independently, medianizes
// independently, and records slash decisions.
//
// It never executes a slash. Deciding and executing are separate steps with separate failure
// modes, and the decision has to be durable before the transaction is attempted — otherwise a
// crash between them loses the reason a node's stake moved.
type Aggregator struct {
	store    AggregatorStore
	verifier SignatureVerifier
	log      zerolog.Logger
	opts     AggregatorOptions
}

func NewAggregator(store AggregatorStore, verifier SignatureVerifier, log zerolog.Logger, opts AggregatorOptions) *Aggregator {
	if opts.OutlierThreshBps == 0 {
		opts.OutlierThreshBps = 500
	}
	if opts.BatchSize == 0 {
		opts.BatchSize = 20
	}
	if opts.MaxSubmissions == 0 {
		opts.MaxSubmissions = 64
	}
	return &Aggregator{store: store, verifier: verifier, log: log, opts: opts}
}

// Step analyses one batch of unanalysed rounds and reports how many it processed.
func (a *Aggregator) Step(ctx context.Context) (int, error) {
	rounds, err := a.store.UnaggregatedRounds(ctx, a.opts.ChainID, a.opts.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("load unaggregated rounds: %w", err)
	}

	for _, round := range rounds {
		if err := a.analyse(ctx, round); err != nil {
			return 0, fmt.Errorf("analyse round %s: %w", round.RoundID, err)
		}
	}
	return len(rounds), nil
}

func (a *Aggregator) analyse(ctx context.Context, round types.OracleRound) error {
	submissions, err := a.store.ListOracleSubmissions(ctx, a.opts.ChainID, round.RoundID, a.opts.MaxSubmissions, 0)
	if err != nil {
		return err
	}

	valid, invalid, unverifiable := a.partitionBySignature(ctx, round, submissions)

	// The median covers verified submissions plus those that could not be checked. Excluding the
	// unverifiable ones would make the service's number diverge from the contract's for a reason
	// that is not misbehaviour, turning every historical round into a false mismatch alert.
	counted := append(append([]types.OracleSubmission{}, valid...), unverifiable...)
	values := make([]types.Raw, len(counted))
	for i, sub := range counted {
		values[i] = sub.Value
	}
	serviceValue := Median(values)

	mismatch := round.State == "settled" && serviceValue.String() != round.AggregatedValue.String()
	if mismatch {
		// An alert, never a correction. The chain is authoritative; this says the two disagree.
		a.log.Error().
			Str("round", round.RoundID.String()).
			Str("chain_value", round.AggregatedValue.String()).
			Str("service_value", serviceValue.String()).
			Int("verified", len(valid)).
			Int("unverifiable", len(unverifiable)).
			Msg("settled value disagrees with independent median")
	}

	outliers := DetectOutliers(counted, serviceValue, a.opts.OutlierThreshBps)
	outlierNodes := make(map[string]*big.Int, len(outliers))
	for _, o := range outliers {
		outlierNodes[o.Node] = o.DeviationBps
	}

	for _, sub := range valid {
		_, isOutlier := outlierNodes[sub.Node]
		if err := a.store.RecordSubmissionVerification(ctx, a.opts.ChainID, round.RoundID, sub.Node, true, isOutlier); err != nil {
			return err
		}
	}
	for _, sub := range unverifiable {
		_, isOutlier := outlierNodes[sub.Node]
		if err := a.store.RecordSubmissionVerification(ctx, a.opts.ChainID, round.RoundID, sub.Node, false, isOutlier); err != nil {
			return err
		}
	}
	for _, sub := range invalid {
		if err := a.store.RecordSubmissionVerification(ctx, a.opts.ChainID, round.RoundID, sub.Node, false, false); err != nil {
			return err
		}
	}

	for _, sub := range invalid {
		if err := a.decide(ctx, round, sub.Node, ReasonInvalidSignature, penaltyInvalidSignatureBps); err != nil {
			return err
		}
	}
	for _, o := range outliers {
		if err := a.decide(ctx, round, o.Node, ReasonOutlier, penaltyOutlierBps); err != nil {
			return err
		}
	}

	// Last, so a crash mid-analysis leaves the round unmarked and it is retried rather than
	// skipped. Every write above is idempotent for exactly that reason.
	return a.store.MarkRoundAggregated(ctx, a.opts.ChainID, round.RoundID, serviceValue, mismatch, time.Now().UTC())
}

// partitionBySignature splits submissions three ways. Unverifiable is distinct from invalid: a
// signature this service could not check is not evidence of misbehaviour, and slashing for it would
// punish a node for an indexing gap.
func (a *Aggregator) partitionBySignature(ctx context.Context, round types.OracleRound, submissions []types.OracleSubmission) (valid, invalid, unverifiable []types.OracleSubmission) {
	for _, sub := range submissions {
		signature, err := a.store.SubmissionSignature(ctx, a.opts.ChainID, round.RoundID, sub.Node)
		if err != nil {
			a.log.Error().Err(err).Str("node", sub.Node).Msg("could not load stored signature")
			invalid = append(invalid, sub)
			continue
		}

		// A submission indexed before the nonce was emitted cannot be verified. Treated as
		// unverifiable rather than assumed to be zero, which would reject every historical row as
		// a forged signature and slash the nodes that produced them.
		if !sub.NonceKnown {
			a.log.Warn().
				Str("round", round.RoundID.String()).
				Str("node", sub.Node).
				Msg("submission predates the emitted nonce; skipping verification")
			unverifiable = append(unverifiable, sub)
			continue
		}

		ok, err := a.verifier.Verify(a.opts.ChainID, round.RoundID, round.FeedID, sub.Value, sub.Node, sub.Nonce, signature)
		if err != nil || !ok {
			a.log.Warn().Err(err).
				Str("round", round.RoundID.String()).
				Str("node", sub.Node).
				Msg("stored signature did not verify off-chain")
			invalid = append(invalid, sub)
			continue
		}
		valid = append(valid, sub)
	}
	return valid, invalid, unverifiable
}

// decide writes the penalty to Postgres. Nothing is sent to a chain here: the audit trail must
// survive the transaction failing, so it is written first and by a different step.
func (a *Aggregator) decide(ctx context.Context, round types.OracleRound, nodeAddress, reason string, penaltyBps int64) error {
	node, err := a.store.OracleNode(ctx, a.opts.ChainID, nodeAddress)
	if err != nil {
		return fmt.Errorf("load node %s: %w", nodeAddress, err)
	}

	amount := new(big.Int).Mul(node.StakedAmount.Big(), big.NewInt(penaltyBps))
	amount.Div(amount, big.NewInt(10_000))
	if amount.Sign() == 0 {
		// A node whose stake rounds the penalty to nothing is already below any meaningful floor.
		// Recording a zero-amount slash would fail the CHECK constraint and stall the batch.
		return nil
	}

	a.log.Info().
		Str("round", round.RoundID.String()).
		Str("node", nodeAddress).
		Str("reason", reason).
		Str("amount", amount.String()).
		Msg("slash decided")

	return a.store.RecordSlashDecision(ctx, SlashDecision{
		ChainID: a.opts.ChainID,
		Node:    nodeAddress,
		RoundID: round.RoundID,
		Amount:  types.NewRaw(amount),
		Reason:  reason,
	})
}
