package zk

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	testChainID = int64(31337)
	treeAddr    = "0x0165878a594ca255338adfa4d48449f69242eb8f"
	gateAddr    = "0xa513e6e4b8f2a923d98304ec87f64353c4d5c853"
)

type fakeReader struct {
	gate   types.ZkGateMetadata
	set    types.AnonymitySet
	leaves []types.Commitment
	err    error

	gateCalls  int
	setCalls   int
	leafCalls  int
	spentCalls int
}

func (f *fakeReader) ZkGate(context.Context, int64) (types.ZkGateMetadata, error) {
	f.gateCalls++
	return f.gate, f.err
}

func (f *fakeReader) AnonymitySet(context.Context, int64, string) (types.AnonymitySet, error) {
	f.setCalls++
	return f.set, f.err
}

func (f *fakeReader) ListCommitments(context.Context, int64, string, int, int) ([]types.Commitment, error) {
	f.leafCalls++
	return f.leaves, f.err
}

func (f *fakeReader) ListPrivateActions(context.Context, int64, string, int, int) ([]types.PrivateAction, error) {
	return nil, f.err
}

func (f *fakeReader) NullifierSpent(context.Context, int64, string, string) (bool, error) {
	f.spentCalls++
	return false, f.err
}

func (f *fakeReader) ListZkActions(context.Context, int64, string, int, int) ([]types.ZkAction, error) {
	return nil, f.err
}

type fakeCache struct {
	values map[string][]byte
	sets   int
}

func newFakeCache() *fakeCache { return &fakeCache{values: map[string][]byte{}} }

func (c *fakeCache) GetBytes(_ context.Context, key string) ([]byte, error) {
	return c.values[key], nil
}

func (c *fakeCache) SetBytes(_ context.Context, key string, value []byte, _ time.Duration) error {
	c.sets++
	c.values[key] = value
	return nil
}

func (c *fakeCache) AcquireFill(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}

func newService(store Reader, c Cache) *Service {
	return NewService(store, c, zerolog.New(io.Discard), testChainID)
}

func wired() *fakeReader {
	return &fakeReader{
		gate: types.ZkGateMetadata{ChainID: testChainID, Address: gateAddr, TreeAddress: treeAddr},
	}
}

// The gate's wiring changes at deployment, so it is cached.
func TestTheGateIsCachedAfterTheFirstRead(t *testing.T) {
	store := wired()
	svc := newService(store, newFakeCache())
	ctx := context.Background()

	first, err := svc.Gate(ctx)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if _, err := svc.Gate(ctx); err != nil {
		t.Fatalf("second read: %v", err)
	}

	if store.gateCalls != 1 {
		t.Errorf("store read %d times, want 1", store.gateCalls)
	}
	if first.TreeAddress != treeAddr {
		t.Errorf("tree = %s", first.TreeAddress)
	}
}

// A path built from a stale commitment list produces a proof that verifies against no root the
// contract holds, and a stale spent-nullifier answer sends a client to pay for a proof it cannot
// use. Neither is cached.
func TestNothingThatFeedsAProofIsCached(t *testing.T) {
	store := wired()
	c := newFakeCache()
	svc := newService(store, c)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := svc.Commitments(ctx, treeAddr, 10, 0); err != nil {
			t.Fatalf("commitments: %v", err)
		}
		if _, err := svc.NullifierSpent(ctx, gateAddr, "0xaa"); err != nil {
			t.Fatalf("nullifier: %v", err)
		}
		if _, err := svc.AnonymitySet(ctx, treeAddr); err != nil {
			t.Fatalf("anonymity set: %v", err)
		}
	}

	if store.leafCalls != 3 {
		t.Errorf("commitments read %d times, want 3 — a stale path is an unusable proof", store.leafCalls)
	}
	if store.spentCalls != 3 {
		t.Errorf("nullifier read %d times, want 3", store.spentCalls)
	}
	if store.setCalls != 3 {
		t.Errorf("anonymity set read %d times, want 3", store.setCalls)
	}

	// Only the gate may ever be written to cache.
	if c.sets > 1 {
		t.Errorf("cache written %d times; only the gate should be cached", c.sets)
	}
}

func TestANilCacheReadsThrough(t *testing.T) {
	store := wired()
	svc := NewService(store, nil, zerolog.New(io.Discard), testChainID)

	for i := 0; i < 2; i++ {
		if _, err := svc.Gate(context.Background()); err != nil {
			t.Fatalf("gate: %v", err)
		}
	}
	if store.gateCalls != 2 {
		t.Errorf("store read %d times, want 2", store.gateCalls)
	}
}

func TestStoreErrorsPropagate(t *testing.T) {
	sentinel := errors.New("boom")
	store := &fakeReader{err: sentinel}
	svc := newService(store, newFakeCache())
	ctx := context.Background()

	if _, err := svc.Gate(ctx); !errors.Is(err, sentinel) {
		t.Errorf("gate error = %v", err)
	}
	if _, err := svc.Commitments(ctx, treeAddr, 10, 0); !errors.Is(err, sentinel) {
		t.Errorf("commitments error = %v", err)
	}
	if _, err := svc.AnonymitySet(ctx, treeAddr); !errors.Is(err, sentinel) {
		t.Errorf("anonymity set error = %v", err)
	}
}
