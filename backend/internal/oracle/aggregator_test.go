package oracle

import (
	"context"
	"errors"
	"io"
	"math/big"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	nodeA = "0xaaaa000000000000000000000000000000000001"
	nodeB = "0xbbbb000000000000000000000000000000000002"
	nodeC = "0xcccc000000000000000000000000000000000003"
)

type fakeAggStore struct {
	rounds      []types.OracleRound
	submissions map[string][]types.OracleSubmission
	signatures  map[string][]byte
	nodes       map[string]types.OracleNode

	decisions     []SlashDecision
	verifications []verification
	marked        []markCall
	sigErr        error
}

type verification struct {
	node    string
	valid   bool
	outlier bool
}

type markCall struct {
	roundID  string
	value    string
	mismatch bool
}

func (f *fakeAggStore) UnaggregatedRounds(context.Context, int64, int) ([]types.OracleRound, error) {
	return f.rounds, nil
}

func (f *fakeAggStore) ListOracleSubmissions(_ context.Context, _ int64, roundID types.Raw, _, _ int) ([]types.OracleSubmission, error) {
	return f.submissions[roundID.String()], nil
}

func (f *fakeAggStore) SubmissionSignature(_ context.Context, _ int64, _ types.Raw, node string) ([]byte, error) {
	if f.sigErr != nil {
		return nil, f.sigErr
	}
	return f.signatures[node], nil
}

func (f *fakeAggStore) RecordSubmissionVerification(_ context.Context, _ int64, _ types.Raw, node string, valid, outlier bool) error {
	f.verifications = append(f.verifications, verification{node: node, valid: valid, outlier: outlier})
	return nil
}

func (f *fakeAggStore) RecordSlashDecision(_ context.Context, d SlashDecision) error {
	f.decisions = append(f.decisions, d)
	return nil
}

func (f *fakeAggStore) MarkRoundAggregated(_ context.Context, _ int64, roundID, serviceValue types.Raw, mismatch bool, _ time.Time) error {
	f.marked = append(f.marked, markCall{roundID: roundID.String(), value: serviceValue.String(), mismatch: mismatch})
	return nil
}

func (f *fakeAggStore) OracleNode(_ context.Context, _ int64, address string) (types.OracleNode, error) {
	node, ok := f.nodes[address]
	if !ok {
		return types.OracleNode{}, errors.New("node not found")
	}
	return node, nil
}

// fakeVerifier accepts every signature except those for nodes listed in reject.
type fakeVerifier struct {
	reject map[string]bool
	err    error
	seen   []types.Raw
}

func (v *fakeVerifier) Verify(_ int64, _ types.Raw, _ string, _ types.Raw, node string, nonce types.Raw, _ []byte) (bool, error) {
	v.seen = append(v.seen, nonce)
	if v.err != nil {
		return false, v.err
	}
	return !v.reject[node], nil
}

func submission(t *testing.T, node, value string, nonce int64) types.OracleSubmission {
	t.Helper()
	return types.OracleSubmission{
		Node:       node,
		Value:      raw(t, value),
		Nonce:      types.NewRaw(big.NewInt(nonce)),
		NonceKnown: true,
	}
}

func newAggregator(t *testing.T, store *fakeAggStore, verifier SignatureVerifier) *Aggregator {
	t.Helper()
	return NewAggregator(store, verifier, zerolog.New(io.Discard), AggregatorOptions{
		ChainID:          31337,
		OutlierThreshBps: 500,
	})
}

func settledRound(t *testing.T, id, value string) types.OracleRound {
	t.Helper()
	return types.OracleRound{
		RoundID:         raw(t, id),
		FeedID:          "0xfeed",
		State:           "settled",
		AggregatedValue: raw(t, value),
	}
}

func stakedNodes(t *testing.T, stake string, addresses ...string) map[string]types.OracleNode {
	t.Helper()
	out := map[string]types.OracleNode{}
	for _, a := range addresses {
		out[a] = types.OracleNode{Address: a, StakedAmount: raw(t, stake)}
	}
	return out
}

// The decision must be durable before anything reaches a chain. The aggregator has no chain client
// at all, which is the structural version of that guarantee.
func TestAggregatorRecordsDecisionsWithoutAnyChainCall(t *testing.T) {
	store := &fakeAggStore{
		rounds: []types.OracleRound{settledRound(t, "1", "3000")},
		submissions: map[string][]types.OracleSubmission{
			"1": {
				submission(t, nodeA, "3000", 0),
				submission(t, nodeB, "3000", 0),
				submission(t, nodeC, "9000", 0), // +200%
			},
		},
		nodes: stakedNodes(t, "10000", nodeA, nodeB, nodeC),
	}

	processed, err := newAggregator(t, store, &fakeVerifier{}).Step(context.Background())
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed %d rounds", processed)
	}

	if len(store.decisions) != 1 {
		t.Fatalf("recorded %d decisions, want 1: %+v", len(store.decisions), store.decisions)
	}
	d := store.decisions[0]
	if d.Node != nodeC || d.Reason != ReasonOutlier {
		t.Fatalf("decision = %+v", d)
	}
	// 1% of 10000.
	if d.Amount.String() != "100" {
		t.Errorf("amount = %s, want 100 (1%% of stake)", d.Amount)
	}
}

// The round is marked only after every decision is written, so a crash mid-analysis retries the
// round rather than skipping it.
func TestRoundIsMarkedAfterDecisions(t *testing.T) {
	store := &fakeAggStore{
		rounds: []types.OracleRound{settledRound(t, "1", "3000")},
		submissions: map[string][]types.OracleSubmission{
			"1": {submission(t, nodeA, "3000", 0), submission(t, nodeB, "3000", 0)},
		},
		nodes: stakedNodes(t, "10000", nodeA, nodeB),
	}

	if _, err := newAggregator(t, store, &fakeVerifier{}).Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if len(store.marked) != 1 || store.marked[0].roundID != "1" {
		t.Fatalf("marked = %+v", store.marked)
	}
}

// A disagreement with the settled value is recorded and alerted, never corrected. The chain is
// authoritative.
func TestValueMismatchIsFlaggedNotCorrected(t *testing.T) {
	store := &fakeAggStore{
		rounds: []types.OracleRound{settledRound(t, "1", "9999")}, // contract says something else
		submissions: map[string][]types.OracleSubmission{
			"1": {submission(t, nodeA, "3000", 0), submission(t, nodeB, "3000", 0), submission(t, nodeC, "3000", 0)},
		},
		nodes: stakedNodes(t, "10000", nodeA, nodeB, nodeC),
	}

	if _, err := newAggregator(t, store, &fakeVerifier{}).Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}

	if !store.marked[0].mismatch {
		t.Fatal("a disagreement with the settled value was not flagged")
	}
	if store.marked[0].value != "3000" {
		t.Errorf("service value = %s, want the independently computed 3000", store.marked[0].value)
	}
	if len(store.decisions) != 0 {
		t.Errorf("a mismatch produced slash decisions: %+v", store.decisions)
	}
}

// A signature the contract accepted but this service cannot verify is slashable at 5%.
func TestInvalidSignatureIsSlashed(t *testing.T) {
	store := &fakeAggStore{
		rounds: []types.OracleRound{settledRound(t, "1", "3000")},
		submissions: map[string][]types.OracleSubmission{
			"1": {submission(t, nodeA, "3000", 0), submission(t, nodeB, "3000", 0), submission(t, nodeC, "3000", 0)},
		},
		nodes: stakedNodes(t, "10000", nodeA, nodeB, nodeC),
	}

	verifier := &fakeVerifier{reject: map[string]bool{nodeC: true}}
	if _, err := newAggregator(t, store, verifier).Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}

	if len(store.decisions) != 1 {
		t.Fatalf("decisions = %+v", store.decisions)
	}
	if store.decisions[0].Reason != ReasonInvalidSignature {
		t.Fatalf("reason = %s", store.decisions[0].Reason)
	}
	// 5% of 10000.
	if store.decisions[0].Amount.String() != "500" {
		t.Errorf("amount = %s, want 500 (5%% of stake)", store.decisions[0].Amount)
	}
}

// A submission indexed before the nonce was emitted cannot be verified. That is an indexing gap,
// not misbehaviour, and slashing for it would punish a node for our own migration.
func TestUnverifiableSubmissionIsNotSlashed(t *testing.T) {
	unverifiable := types.OracleSubmission{Node: nodeC, Value: raw(t, "3000"), NonceKnown: false}

	store := &fakeAggStore{
		rounds: []types.OracleRound{settledRound(t, "1", "3000")},
		submissions: map[string][]types.OracleSubmission{
			"1": {submission(t, nodeA, "3000", 0), submission(t, nodeB, "3000", 0), unverifiable},
		},
		nodes: stakedNodes(t, "10000", nodeA, nodeB, nodeC),
	}

	if _, err := newAggregator(t, store, &fakeVerifier{}).Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if len(store.decisions) != 0 {
		t.Fatalf("an unverifiable submission was slashed: %+v", store.decisions)
	}
}

// An unverifiable submission still counts toward the median, or the service's number would diverge
// from the contract's for a reason that is not misbehaviour and every such round would false-alarm.
func TestUnverifiableSubmissionStillCountsTowardTheMedian(t *testing.T) {
	unverifiable := types.OracleSubmission{Node: nodeC, Value: raw(t, "5000"), NonceKnown: false}

	store := &fakeAggStore{
		rounds: []types.OracleRound{settledRound(t, "1", "3000")},
		submissions: map[string][]types.OracleSubmission{
			"1": {submission(t, nodeA, "1000", 0), submission(t, nodeB, "3000", 0), unverifiable},
		},
		nodes: stakedNodes(t, "10000", nodeA, nodeB, nodeC),
	}

	if _, err := newAggregator(t, store, &fakeVerifier{}).Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if store.marked[0].value != "3000" {
		t.Fatalf("service value = %s, want 3000 with all three counted", store.marked[0].value)
	}
	if store.marked[0].mismatch {
		t.Error("flagged a mismatch against the contract that agrees with it")
	}
}

// The nonce that was signed must be the nonce that is verified.
func TestVerificationUsesTheStoredNonce(t *testing.T) {
	store := &fakeAggStore{
		rounds: []types.OracleRound{settledRound(t, "1", "3000")},
		submissions: map[string][]types.OracleSubmission{
			"1": {submission(t, nodeA, "3000", 7)},
		},
		nodes: stakedNodes(t, "10000", nodeA),
	}

	verifier := &fakeVerifier{}
	if _, err := newAggregator(t, store, verifier).Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if len(verifier.seen) != 1 || verifier.seen[0].String() != "7" {
		t.Fatalf("verified with nonce %v, want 7", verifier.seen)
	}
}

// A node whose stake rounds the penalty to zero must not produce a decision: the CHECK constraint
// requires a positive amount, and a failed insert would stall every later round.
func TestZeroPenaltyProducesNoDecision(t *testing.T) {
	store := &fakeAggStore{
		rounds: []types.OracleRound{settledRound(t, "1", "3000")},
		submissions: map[string][]types.OracleSubmission{
			"1": {submission(t, nodeA, "3000", 0), submission(t, nodeB, "3000", 0), submission(t, nodeC, "9000", 0)},
		},
		nodes: map[string]types.OracleNode{
			nodeA: {Address: nodeA, StakedAmount: raw(t, "10000")},
			nodeB: {Address: nodeB, StakedAmount: raw(t, "10000")},
			nodeC: {Address: nodeC, StakedAmount: raw(t, "50")}, // 1% of 50 floors to 0
		},
	}

	if _, err := newAggregator(t, store, &fakeVerifier{}).Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if len(store.decisions) != 0 {
		t.Fatalf("recorded a zero-amount decision: %+v", store.decisions)
	}
}

func TestOutlierIsRecordedOnTheSubmission(t *testing.T) {
	store := &fakeAggStore{
		rounds: []types.OracleRound{settledRound(t, "1", "3000")},
		submissions: map[string][]types.OracleSubmission{
			"1": {submission(t, nodeA, "3000", 0), submission(t, nodeB, "3000", 0), submission(t, nodeC, "9000", 0)},
		},
		nodes: stakedNodes(t, "10000", nodeA, nodeB, nodeC),
	}

	if _, err := newAggregator(t, store, &fakeVerifier{}).Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}

	var flagged int
	for _, v := range store.verifications {
		if v.outlier {
			flagged++
			if v.node != nodeC {
				t.Errorf("flagged %s as an outlier", v.node)
			}
		}
	}
	if flagged != 1 {
		t.Fatalf("flagged %d submissions as outliers, want 1", flagged)
	}
}
