package governance

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const testChainID = int64(31337)

type fakeReader struct {
	proposals []types.Proposal
	votes     []types.Vote
	governor  types.GovernorMetadata
	err       error

	governorCalls int
	proposalCalls int
	gotState      string
}

func (f *fakeReader) ListProposals(_ context.Context, _ int64, state string, _, _ int) ([]types.Proposal, error) {
	f.proposalCalls++
	f.gotState = state
	return f.proposals, f.err
}

func (f *fakeReader) Proposal(context.Context, int64, types.Raw) (types.Proposal, error) {
	f.proposalCalls++
	if len(f.proposals) == 0 {
		return types.Proposal{}, f.err
	}
	return f.proposals[0], f.err
}

func (f *fakeReader) ListProposalVotes(context.Context, int64, types.Raw, int, int) ([]types.Vote, error) {
	return f.votes, f.err
}

func (f *fakeReader) ListVotesByVoter(context.Context, int64, string, int, int) ([]types.Vote, error) {
	return f.votes, f.err
}

func (f *fakeReader) Governor(context.Context, int64) (types.GovernorMetadata, error) {
	f.governorCalls++
	return f.governor, f.err
}

type fakeCache struct {
	values     map[string][]byte
	sets       int
	refuseFill bool
	setErr     error
	getErr     error
}

func newFakeCache() *fakeCache { return &fakeCache{values: map[string][]byte{}} }

func (c *fakeCache) GetBytes(_ context.Context, key string) ([]byte, error) {
	if c.getErr != nil {
		return nil, c.getErr
	}
	return c.values[key], nil
}

func (c *fakeCache) SetBytes(_ context.Context, key string, value []byte, _ time.Duration) error {
	if c.setErr != nil {
		return c.setErr
	}
	c.sets++
	c.values[key] = value
	return nil
}

func (c *fakeCache) AcquireFill(context.Context, string, time.Duration) (bool, error) {
	return !c.refuseFill, nil
}

func newService(store Reader, c Cache) *Service {
	return NewService(store, c, zerolog.New(io.Discard), testChainID)
}

// Governor metadata changes at deployment, so it is cached. A second read must not touch the store.
func TestGovernorIsServedFromCacheOnTheSecondRead(t *testing.T) {
	store := &fakeReader{governor: types.GovernorMetadata{
		ChainID: testChainID, Address: "0xabc", TokenAddress: "0xdef", TokenDecimals: 18,
	}}
	svc := newService(store, newFakeCache())
	ctx := context.Background()

	first, err := svc.Governor(ctx)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	second, err := svc.Governor(ctx)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}

	if store.governorCalls != 1 {
		t.Errorf("store read %d times, want 1", store.governorCalls)
	}
	if first != second {
		t.Errorf("cached value differs: %+v vs %+v", first, second)
	}
	if second.TokenDecimals != 18 {
		t.Errorf("token decimals lost through the cache: %d", second.TokenDecimals)
	}
}

// A proposal's remaining delay is what an operator is watching. A stale answer about what is about
// to execute is worse than a slow one, so proposals and votes are never served from cache.
func TestProposalsAndVotesAreNeverCached(t *testing.T) {
	store := &fakeReader{proposals: []types.Proposal{{ProposalID: types.Raw{}, State: types.ProposalStateQueued}}}
	c := newFakeCache()
	svc := newService(store, c)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := svc.Proposals(ctx, types.ProposalStateQueued, 10, 0); err != nil {
			t.Fatalf("proposals: %v", err)
		}
	}

	if store.proposalCalls != 3 {
		t.Errorf("store read %d times, want 3 — proposals must not be cached", store.proposalCalls)
	}
	if c.sets != 0 {
		t.Errorf("proposals were written to cache %d times", c.sets)
	}
}

func TestStateFilterReachesTheStore(t *testing.T) {
	store := &fakeReader{}
	svc := newService(store, newFakeCache())

	if _, err := svc.Proposals(context.Background(), types.ProposalStateExecuted, 10, 0); err != nil {
		t.Fatalf("proposals: %v", err)
	}
	if store.gotState != types.ProposalStateExecuted {
		t.Errorf("state = %q, want %q", store.gotState, types.ProposalStateExecuted)
	}
}

// A cache that refuses or fails a write must never fail a read that already succeeded.
func TestCacheFailuresDoNotFailTheRead(t *testing.T) {
	governor := types.GovernorMetadata{ChainID: testChainID, Address: "0xabc", TokenDecimals: 18}

	for name, c := range map[string]*fakeCache{
		"fill refused": {values: map[string][]byte{}, refuseFill: true},
		"set fails":    {values: map[string][]byte{}, setErr: errors.New("redis down")},
		"get fails":    {values: map[string][]byte{}, getErr: errors.New("redis down")},
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeReader{governor: governor}
			got, err := newService(store, c).Governor(context.Background())
			if err != nil {
				t.Fatalf("read failed because of the cache: %v", err)
			}
			if got.Address != governor.Address {
				t.Errorf("address = %s", got.Address)
			}
		})
	}
}

// A nil cache is a supported configuration: the service reads through.
func TestNilCacheReadsThrough(t *testing.T) {
	store := &fakeReader{governor: types.GovernorMetadata{ChainID: testChainID, Address: "0xabc"}}
	svc := NewService(store, nil, zerolog.New(io.Discard), testChainID)

	for i := 0; i < 2; i++ {
		if _, err := svc.Governor(context.Background()); err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	if store.governorCalls != 2 {
		t.Errorf("store read %d times, want 2", store.governorCalls)
	}
}

func TestStoreErrorsPropagate(t *testing.T) {
	sentinel := errors.New("boom")
	store := &fakeReader{err: sentinel}
	svc := newService(store, newFakeCache())

	if _, err := svc.Governor(context.Background()); !errors.Is(err, sentinel) {
		t.Errorf("governor error = %v, want %v", err, sentinel)
	}
	if _, err := svc.Proposals(context.Background(), "", 10, 0); !errors.Is(err, sentinel) {
		t.Errorf("proposals error = %v, want %v", err, sentinel)
	}
}
