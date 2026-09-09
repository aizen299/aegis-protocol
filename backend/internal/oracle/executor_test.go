package oracle

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

type fakeExecStore struct {
	pending   []PendingSlash
	submitted []string
	abandoned []abandonCall
	claimArgs struct{ limit, maxAttempts int }
}

type abandonCall struct {
	id     string
	reason string
}

func (f *fakeExecStore) ClaimPendingSlashes(_ context.Context, _ int64, limit, maxAttempts int) ([]PendingSlash, error) {
	f.claimArgs.limit, f.claimArgs.maxAttempts = limit, maxAttempts
	return f.pending, nil
}

func (f *fakeExecStore) MarkSlashSubmitted(_ context.Context, id, _ string, _ time.Time) error {
	f.submitted = append(f.submitted, id)
	return nil
}

func (f *fakeExecStore) MarkSlashAbandoned(_ context.Context, id, reason string, _ time.Time) error {
	f.abandoned = append(f.abandoned, abandonCall{id: id, reason: reason})
	return nil
}

type fakeSlasher struct {
	err    error
	errFor map[string]error
	calls  []string
}

func (f *fakeSlasher) Slash(_ context.Context, node string, roundID, _ types.Raw, _ string) (string, error) {
	f.calls = append(f.calls, node+"@"+roundID.String())
	if err, ok := f.errFor[node]; ok {
		return "", err
	}
	if f.err != nil {
		return "", f.err
	}
	return "0xtx", nil
}

func pending(t *testing.T, id, node string, attempts int) PendingSlash {
	t.Helper()
	return PendingSlash{
		ID:       id,
		Node:     node,
		RoundID:  raw(t, "7"),
		Amount:   raw(t, "100"),
		Reason:   ReasonOutlier,
		Attempts: attempts,
	}
}

func newExecutor(t *testing.T, store *fakeExecStore, slasher Slasher) *Executor {
	t.Helper()
	return NewExecutor(store, slasher, zerolog.New(io.Discard), ExecutorOptions{
		ChainID:     31337,
		MaxAttempts: 3,
	})
}

func TestExecutorSubmitsPendingDecisions(t *testing.T) {
	store := &fakeExecStore{pending: []PendingSlash{pending(t, "a", nodeA, 0), pending(t, "b", nodeB, 0)}}
	slasher := &fakeSlasher{}

	handled, err := newExecutor(t, store, slasher).Step(context.Background())
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if handled != 2 {
		t.Fatalf("handled %d, want 2", handled)
	}
	if len(store.submitted) != 2 {
		t.Fatalf("submitted %v", store.submitted)
	}
	if len(slasher.calls) != 2 {
		t.Fatalf("chain calls %v", slasher.calls)
	}
}

// Marked submitted, not executed. A sent transaction can still be dropped, so the audit trail must
// reflect what the chain confirmed — which the indexer records when it sees NodeSlashed.
func TestSubmissionDoesNotClaimExecution(t *testing.T) {
	store := &fakeExecStore{pending: []PendingSlash{pending(t, "a", nodeA, 0)}}

	if _, err := newExecutor(t, store, &fakeSlasher{}).Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if len(store.submitted) != 1 {
		t.Fatal("decision was not marked submitted")
	}
	if len(store.abandoned) != 0 {
		t.Fatalf("decision was abandoned: %+v", store.abandoned)
	}
}

// The contract's per-round guard rejecting a retry means an earlier attempt landed. That satisfies
// the decision rather than failing it — retrying would only fail again forever.
func TestAlreadySlashedClosesTheDecision(t *testing.T) {
	store := &fakeExecStore{pending: []PendingSlash{pending(t, "a", nodeA, 1)}}
	slasher := &fakeSlasher{err: ErrAlreadySlashed}

	if _, err := newExecutor(t, store, slasher).Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if len(store.abandoned) != 1 {
		t.Fatalf("abandoned %+v", store.abandoned)
	}
	if len(store.submitted) != 0 {
		t.Fatal("a decision the chain rejected was marked submitted")
	}
}

// One node's failure must not hold up another's penalty.
func TestOneFailureDoesNotAbandonTheBatch(t *testing.T) {
	store := &fakeExecStore{pending: []PendingSlash{
		pending(t, "a", nodeA, 0),
		pending(t, "b", nodeB, 0),
		pending(t, "c", nodeC, 0),
	}}
	slasher := &fakeSlasher{errFor: map[string]error{nodeB: errors.New("nonce too low")}}

	handled, err := newExecutor(t, store, slasher).Step(context.Background())
	if err != nil {
		t.Fatalf("step returned an error for one failed decision: %v", err)
	}
	if handled != 2 {
		t.Fatalf("handled %d, want the two that succeeded", handled)
	}
	if len(store.submitted) != 2 {
		t.Fatalf("submitted %v", store.submitted)
	}
}

// A decision that keeps failing becomes something an operator can see, rather than a loop.
func TestDecisionIsAbandonedAfterMaxAttempts(t *testing.T) {
	store := &fakeExecStore{pending: []PendingSlash{pending(t, "a", nodeA, 2)}} // third attempt
	slasher := &fakeSlasher{err: errors.New("insufficient funds for gas")}

	if _, err := newExecutor(t, store, slasher).Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if len(store.abandoned) != 1 {
		t.Fatalf("abandoned %+v, want the exhausted decision closed", store.abandoned)
	}
	if store.abandoned[0].reason == "" {
		t.Error("abandoned without recording why")
	}
}

func TestDecisionIsRetriedBeforeMaxAttempts(t *testing.T) {
	store := &fakeExecStore{pending: []PendingSlash{pending(t, "a", nodeA, 0)}}
	slasher := &fakeSlasher{err: errors.New("temporary rpc failure")}

	if _, err := newExecutor(t, store, slasher).Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if len(store.abandoned) != 0 {
		t.Fatalf("abandoned on the first failure: %+v", store.abandoned)
	}
	if len(store.submitted) != 0 {
		t.Fatal("a failed submission was marked submitted")
	}
}

// The executor makes no judgements: it submits exactly the amount and reason that were decided.
func TestExecutorSubmitsTheDecidedAmountUnchanged(t *testing.T) {
	decision := pending(t, "a", nodeA, 0)
	decision.Amount = raw(t, "123456789012345678901234567890")

	store := &fakeExecStore{pending: []PendingSlash{decision}}
	var seen types.Raw
	slasher := &recordingSlasher{onSlash: func(amount types.Raw) { seen = amount }}

	if _, err := newExecutor(t, store, slasher).Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if seen.String() != "123456789012345678901234567890" {
		t.Fatalf("submitted %s, want the decided amount unchanged", seen)
	}
}

type recordingSlasher struct {
	onSlash func(types.Raw)
}

func (r *recordingSlasher) Slash(_ context.Context, _ string, _, amount types.Raw, _ string) (string, error) {
	r.onSlash(amount)
	return "0xtx", nil
}

func TestClaimIsBoundedByMaxAttempts(t *testing.T) {
	store := &fakeExecStore{}

	if _, err := newExecutor(t, store, &fakeSlasher{}).Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if store.claimArgs.maxAttempts != 3 {
		t.Fatalf("claimed with maxAttempts %d, want 3", store.claimArgs.maxAttempts)
	}
}
