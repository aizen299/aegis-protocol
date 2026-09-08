package indexer

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const testChainID = int64(31337)

type fakeClient struct {
	mu       sync.Mutex
	head     uint64
	confirms uint64
	ranges   [][2]uint64
	events   map[uint64][]chain.Event
	headErr  error
}

func (f *fakeClient) ChainID() int64            { return testChainID }
func (f *fakeClient) ConfirmationDepth() uint64 { return f.confirms }
func (f *fakeClient) Close()                    {}

func (f *fakeClient) Head(context.Context) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.headErr != nil {
		return 0, f.headErr
	}
	return f.head, nil
}

func (f *fakeClient) LogsInRange(_ context.Context, from, to uint64, _ []chain.Filter) ([]chain.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ranges = append(f.ranges, [2]uint64{from, to})

	var out []chain.Event
	for b := from; b <= to; b++ {
		out = append(out, f.events[b]...)
	}
	return out, nil
}

func (f *fakeClient) EncodeIdentity(id types.Identity) string { return id.EVMHex() }

func (f *fakeClient) DecodeIdentity(s string) (types.Identity, error) {
	return types.IdentityFromEVMHex(s)
}

func (f *fakeClient) TokenMetadata(context.Context, types.Identity) (chain.TokenMeta, error) {
	return chain.TokenMeta{Decimals: 18, Symbol: "MOCK"}, nil
}

func (f *fakeClient) seenRanges() [][2]uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][2]uint64, len(f.ranges))
	copy(out, f.ranges)
	return out
}

type fakeCursors struct {
	mu      sync.Mutex
	block   uint64
	found   bool
	saves   []uint64
	saveErr error
}

func (f *fakeCursors) LoadCursor(context.Context, string, int64) (uint64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.block, f.found, nil
}

func (f *fakeCursors) SaveCursor(_ context.Context, _ string, _ int64, block uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saves = append(f.saves, block)
	f.block = block
	f.found = true
	return nil
}

func (f *fakeCursors) saved() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uint64, len(f.saves))
	copy(out, f.saves)
	return out
}

type recordingHandler struct {
	mu   sync.Mutex
	seen []chain.Event
	err  error
}

func (h *recordingHandler) Filters() []chain.Filter { return []chain.Filter{{}} }

func (h *recordingHandler) Handle(_ context.Context, ev chain.Event) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.err != nil {
		return h.err
	}
	h.seen = append(h.seen, ev)
	return nil
}

func (h *recordingHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.seen)
}

func newTestIndexer(client chain.Client, cursors CursorStore, opts Options, hs ...Handler) *Indexer {
	if opts.ServiceName == "" {
		opts.ServiceName = "test"
	}
	if opts.BatchSize == 0 {
		opts.BatchSize = 100
	}
	return New(client, cursors, zerolog.New(io.Discard), opts, hs...)
}

func TestStepStopsAtConfirmationDepth(t *testing.T) {
	client := &fakeClient{head: 100, confirms: 12}
	cursors := &fakeCursors{}
	idx := newTestIndexer(client, cursors, Options{})

	if err := idx.restoreCursor(context.Background()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := idx.Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}

	ranges := client.seenRanges()
	if len(ranges) != 1 {
		t.Fatalf("expected one range, got %v", ranges)
	}
	if ranges[0][1] != 88 {
		t.Fatalf("processed up to block %d; must not exceed head-confirms = 88", ranges[0][1])
	}
}

func TestStepDoesNothingWhenHeadBelowConfirmations(t *testing.T) {
	client := &fakeClient{head: 5, confirms: 12}
	idx := newTestIndexer(client, &fakeCursors{}, Options{})

	advanced, err := idx.Step(context.Background())
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if advanced {
		t.Fatal("indexer advanced on a chain shorter than the confirmation window")
	}
	if len(client.seenRanges()) != 0 {
		t.Fatal("indexer queried logs before any block was confirmed")
	}
}

func TestStepRespectsBatchSize(t *testing.T) {
	client := &fakeClient{head: 1000, confirms: 0}
	idx := newTestIndexer(client, &fakeCursors{}, Options{BatchSize: 50})

	if _, err := idx.Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}

	ranges := client.seenRanges()
	if ranges[0] != [2]uint64{1, 50} {
		t.Fatalf("got range %v, want [1 50]", ranges[0])
	}
}

func TestRestoreCursorResumesFromPersisted(t *testing.T) {
	client := &fakeClient{head: 500, confirms: 0}
	cursors := &fakeCursors{block: 300, found: true}
	idx := newTestIndexer(client, cursors, Options{StartBlock: 0, BatchSize: 10})

	if err := idx.restoreCursor(context.Background()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if idx.Cursor() != 300 {
		t.Fatalf("cursor = %d, want 300", idx.Cursor())
	}

	if _, err := idx.Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if got := client.seenRanges()[0]; got != [2]uint64{301, 310} {
		t.Fatalf("got range %v, want [301 310]; a restart must not re-scan from genesis", got)
	}
}

func TestRestoreCursorSeedsFromStartBlockWhenAbsent(t *testing.T) {
	cursors := &fakeCursors{}
	idx := newTestIndexer(&fakeClient{head: 500}, cursors, Options{StartBlock: 120})

	if err := idx.restoreCursor(context.Background()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if idx.Cursor() != 120 {
		t.Fatalf("cursor = %d, want 120", idx.Cursor())
	}
	if saved := cursors.saved(); len(saved) != 1 || saved[0] != 120 {
		t.Fatalf("start block should be persisted immediately, got %v", saved)
	}
}

func TestCursorNotAdvancedWhenHandlerFails(t *testing.T) {
	client := &fakeClient{
		head:   50,
		events: map[uint64][]chain.Event{10: {{ChainID: testChainID, BlockNumber: 10, Name: "Deposited"}}},
	}
	cursors := &fakeCursors{}
	handler := &recordingHandler{err: errors.New("write failed")}
	idx := newTestIndexer(client, cursors, Options{}, handler)

	if _, err := idx.Step(context.Background()); err == nil {
		t.Fatal("expected step to surface the handler error")
	}
	if len(cursors.saved()) != 0 {
		t.Fatal("cursor advanced despite a failed handler; the batch would be lost on restart")
	}
	if idx.Cursor() != 0 {
		t.Fatalf("in-memory cursor = %d, want 0", idx.Cursor())
	}
}

func TestReplayDeliversSameEventsAgain(t *testing.T) {
	events := map[uint64][]chain.Event{
		10: {{ChainID: testChainID, BlockNumber: 10, Name: "Deposited"}},
	}
	handler := &recordingHandler{}

	// Two independent runs over the same range: the indexer offers no dedup of its own, which is
	// why handlers must be idempotent.
	for range 2 {
		client := &fakeClient{head: 50, events: events}
		idx := newTestIndexer(client, &fakeCursors{}, Options{}, handler)
		if _, err := idx.Step(context.Background()); err != nil {
			t.Fatalf("step: %v", err)
		}
	}

	if handler.count() != 2 {
		t.Fatalf("handler saw %d events, want 2", handler.count())
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	client := &fakeClient{head: 10, confirms: 12}
	idx := newTestIndexer(client, &fakeCursors{}, Options{PollInterval: 10 * time.Millisecond})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- idx.Run(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil on cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
