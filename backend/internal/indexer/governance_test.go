package indexer

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	governorHex  = "0x8a791620dd6260079bf849dc5567adc3f2fdc318"
	voteTokenHex = "0xa513e6e4b8f2a923d98304ec87f64353c4d5c853"
	timelockHex  = "0x2279b7a0a67db372996a5fab50d91eaa73d2ebe6"
	voterHex     = "0x70997970c51812dc3a010c7d01b50e0d17dc79c8"
)

type fakeGovernanceStore struct {
	assets     []types.AssetMetadata
	governors  []types.GovernorMetadata
	proposals  []db.GovernanceProposal
	votes      []db.GovernanceVote
	queued     map[string]queuedRecord
	executed   []string
	dispatched []string
	cancelled  []string
}

type queuedRecord struct {
	operationID  types.Raw
	executableAt time.Time
}

func newFakeGovernanceStore() *fakeGovernanceStore {
	return &fakeGovernanceStore{queued: map[string]queuedRecord{}}
}

func (f *fakeGovernanceStore) UpsertAsset(_ context.Context, a types.AssetMetadata) error {
	f.assets = append(f.assets, a)
	return nil
}

func (f *fakeGovernanceStore) UpsertGovernor(_ context.Context, g types.GovernorMetadata) error {
	f.governors = append(f.governors, g)
	return nil
}

func (f *fakeGovernanceStore) InsertGovernanceProposal(_ context.Context, p db.GovernanceProposal) error {
	f.proposals = append(f.proposals, p)
	return nil
}

func (f *fakeGovernanceStore) InsertGovernanceVote(_ context.Context, v db.GovernanceVote) error {
	f.votes = append(f.votes, v)
	return nil
}

func (f *fakeGovernanceStore) MarkProposalQueued(_ context.Context, _ int64, proposalID, operationID types.Raw, executableAt, _ time.Time) error {
	f.queued[proposalID.String()] = queuedRecord{operationID: operationID, executableAt: executableAt}
	return nil
}

func (f *fakeGovernanceStore) MarkProposalExecuted(_ context.Context, _ int64, proposalID types.Raw, _ time.Time) error {
	f.executed = append(f.executed, proposalID.String())
	return nil
}

func (f *fakeGovernanceStore) MarkProposalDispatched(_ context.Context, _ int64, proposalID types.Raw, _ time.Time) error {
	f.dispatched = append(f.dispatched, proposalID.String())
	return nil
}

func (f *fakeGovernanceStore) MarkProposalCancelled(_ context.Context, _ int64, proposalID types.Raw, _ time.Time) error {
	f.cancelled = append(f.cancelled, proposalID.String())
	return nil
}

type fakeGovernorMeta struct {
	token    types.Identity
	timelock types.Identity
	err      error
	calls    int
}

func (f *fakeGovernorMeta) GovernorMetadata(context.Context, types.Identity) (types.Identity, types.Identity, error) {
	f.calls++
	if f.err != nil {
		return types.Identity{}, types.Identity{}, f.err
	}
	return f.token, f.timelock, nil
}

func newGovernanceHandler(t *testing.T, store GovernanceStore, meta GovernorMetadataReader) *GovernanceHandler {
	t.Helper()
	return &GovernanceHandler{
		contract:     mustID(t, governorHex),
		encode:       func(id types.Identity) string { return id.EVMHex() },
		resolver:     &fakeClient{},
		governorMeta: meta,
		store:        store,
		chainID:      testChainID,
	}
}

func newGovernorMeta(t *testing.T) *fakeGovernorMeta {
	t.Helper()
	return &fakeGovernorMeta{token: mustID(t, voteTokenHex), timelock: mustID(t, timelockHex)}
}

func governanceEvent(t *testing.T, name string, payload map[string]any) chain.Event {
	t.Helper()
	return chain.Event{
		ChainID:     testChainID,
		BlockNumber: 99,
		BlockTime:   1735689600,
		TxHash:      [32]byte{0xbe, 0xef},
		LogIndex:    2,
		Contract:    mustID(t, governorHex),
		Name:        name,
		Payload:     payload,
	}
}

func proposalCreatedPayload(t *testing.T, targetChainID int64) map[string]any {
	t.Helper()
	return map[string]any{
		"proposalId":    mustBigInt(t, "1"),
		"proposer":      mustID(t, voterHex),
		"targetChainId": big.NewInt(targetChainID),
		"target":        [32]byte{0x11, 0x22},
		"value":         mustBigInt(t, "0"),
		"payload":       []byte{0xde, 0xad},
		"voteStart":     mustBigInt(t, "1735689600"),
		"voteEnd":       mustBigInt(t, "1736294400"),
		"title":         "Set the value",
		"description":   "because",
	}
}

// The event carries the whole action, so the row is written without any contract read beyond the
// governor's own metadata.
func TestProposalCreatedIsIndexedWithoutReadingTheProposal(t *testing.T) {
	store := newFakeGovernanceStore()
	h := newGovernanceHandler(t, store, newGovernorMeta(t))

	ev := governanceEvent(t, eventProposalCreated, proposalCreatedPayload(t, testChainID))
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if len(store.proposals) != 1 {
		t.Fatalf("proposals = %d, want 1", len(store.proposals))
	}
	p := store.proposals[0]
	if p.Title != "Set the value" || p.Description != "because" {
		t.Errorf("title/description not indexed: %+v", p)
	}
	if p.TargetChainID != testChainID {
		t.Errorf("target chain = %d, want %d", p.TargetChainID, testChainID)
	}
	if string(p.Calldata) != string([]byte{0xde, 0xad}) {
		t.Errorf("calldata = %x", p.Calldata)
	}
	if p.LogIndex != 2 {
		t.Errorf("log index = %d, want 2", p.LogIndex)
	}
}

// The target is stored as the 32 bytes emitted, never narrowed to an address. A remote target does
// not fit one, and truncating here would lose the destination.
func TestProposalTargetIsStoredAsThirtyTwoBytes(t *testing.T) {
	store := newFakeGovernanceStore()
	h := newGovernanceHandler(t, store, newGovernorMeta(t))

	payload := proposalCreatedPayload(t, testChainID+1)
	payload["target"] = [32]byte{0xff, 0xee, 0xdd}

	if err := h.Handle(context.Background(), governanceEvent(t, eventProposalCreated, payload)); err != nil {
		t.Fatalf("handle: %v", err)
	}

	p := store.proposals[0]
	const want = "0xffeedd" + "0000000000000000000000000000000000000000000000000000000000"
	if p.Target != want {
		t.Errorf("target = %s, want %s", p.Target, want)
	}
	if p.TargetChainID != testChainID+1 {
		t.Errorf("remote target chain lost: %d", p.TargetChainID)
	}
}

// The vote token gives every weight its scale, and the proposals table has a foreign key to the
// governor. Recording both before the first row is what keeps a weight from being stored without
// the metadata that makes it readable.
func TestGovernorAndVoteTokenAreRecordedBeforeAnyProposal(t *testing.T) {
	store := newFakeGovernanceStore()
	meta := newGovernorMeta(t)
	h := newGovernanceHandler(t, store, meta)

	ev := governanceEvent(t, eventProposalCreated, proposalCreatedPayload(t, testChainID))
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if len(store.assets) != 1 || store.assets[0].Decimals != 18 {
		t.Fatalf("vote token not recorded with its decimals: %+v", store.assets)
	}
	if len(store.governors) != 1 {
		t.Fatalf("governor not recorded: %+v", store.governors)
	}
	if store.governors[0].TokenAddress != voteTokenHex {
		t.Errorf("vote token = %s, want %s", store.governors[0].TokenAddress, voteTokenHex)
	}
	if store.governors[0].TimelockAddress != timelockHex {
		t.Errorf("timelock = %s, want %s", store.governors[0].TimelockAddress, timelockHex)
	}

	// A second event must not re-read metadata that cannot change.
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("second handle: %v", err)
	}
	if meta.calls != 1 {
		t.Errorf("governor metadata read %d times, want 1", meta.calls)
	}
}

// Without the scale a weight is uninterpretable, so a governor that cannot be read must fail the
// batch rather than write a row that means nothing.
func TestAProposalIsNotIndexedWhenTheGovernorCannotBeRead(t *testing.T) {
	store := newFakeGovernanceStore()
	meta := newGovernorMeta(t)
	meta.err = errors.New("rpc down")
	h := newGovernanceHandler(t, store, meta)

	ev := governanceEvent(t, eventProposalCreated, proposalCreatedPayload(t, testChainID))
	if err := h.Handle(context.Background(), ev); err == nil {
		t.Fatal("expected the batch to fail")
	}
	if len(store.proposals) != 0 {
		t.Errorf("a proposal was written without its scale: %+v", store.proposals)
	}
}

func TestVoteCastIsIndexedWithRawWeight(t *testing.T) {
	store := newFakeGovernanceStore()
	h := newGovernanceHandler(t, store, newGovernorMeta(t))

	const weight = "10000000000000000000000000"
	ev := governanceEvent(t, eventVoteCast, map[string]any{
		"proposalId": mustBigInt(t, "1"),
		"voter":      mustID(t, voterHex),
		"support":    uint8(types.SupportFor),
		"weight":     mustBigInt(t, weight),
		"reason":     "aye",
	})
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if len(store.votes) != 1 {
		t.Fatalf("votes = %d, want 1", len(store.votes))
	}
	v := store.votes[0]
	if v.Weight.String() != weight {
		t.Errorf("weight = %s, want %s (raw, unscaled)", v.Weight, weight)
	}
	if v.Support != types.SupportFor {
		t.Errorf("support = %d, want %d", v.Support, types.SupportFor)
	}
	if v.Voter != voterHex {
		t.Errorf("voter = %s", v.Voter)
	}
	if !v.VotedAt.Equal(time.Unix(1735689600, 0).UTC()) {
		t.Errorf("voted at = %s", v.VotedAt)
	}
}

// The operation id is in the event precisely so this correlation needs no contract read.
func TestProposalQueuedCarriesTheTimelockOperation(t *testing.T) {
	store := newFakeGovernanceStore()
	h := newGovernanceHandler(t, store, newGovernorMeta(t))

	ev := governanceEvent(t, eventProposalQueued, map[string]any{
		"proposalId":   mustBigInt(t, "1"),
		"operationId":  mustBigInt(t, "7"),
		"executableAt": mustBigInt(t, "1736294400"),
	})
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}

	record, ok := store.queued["1"]
	if !ok {
		t.Fatal("proposal was not marked queued")
	}
	if record.operationID.String() != "7" {
		t.Errorf("operation id = %s, want 7", record.operationID)
	}
	if !record.executableAt.Equal(time.Unix(1736294400, 0).UTC()) {
		t.Errorf("executable at = %s", record.executableAt)
	}
}

func TestTerminalTransitionsAreIndexed(t *testing.T) {
	store := newFakeGovernanceStore()
	h := newGovernanceHandler(t, store, newGovernorMeta(t))
	ctx := context.Background()

	for _, name := range []string{eventProposalExecuted, eventProposalDispatched, eventProposalCancelled} {
		ev := governanceEvent(t, name, map[string]any{"proposalId": mustBigInt(t, "1")})
		if err := h.Handle(ctx, ev); err != nil {
			t.Fatalf("handle %s: %v", name, err)
		}
	}

	if len(store.executed) != 1 || len(store.dispatched) != 1 || len(store.cancelled) != 1 {
		t.Fatalf("transitions not recorded: executed=%v dispatched=%v cancelled=%v",
			store.executed, store.dispatched, store.cancelled)
	}
}

// A malformed payload must fail loudly. Writing a partial row would leave a proposal in the API
// that does not match the chain.
func TestMalformedGovernanceEventsFail(t *testing.T) {
	store := newFakeGovernanceStore()
	h := newGovernanceHandler(t, store, newGovernorMeta(t))

	payload := proposalCreatedPayload(t, testChainID)
	delete(payload, "title")

	if err := h.Handle(context.Background(), governanceEvent(t, eventProposalCreated, payload)); err == nil {
		t.Fatal("expected a missing field to fail the batch")
	}
	if len(store.proposals) != 0 {
		t.Errorf("a partial proposal was written: %+v", store.proposals)
	}
}

// Events from another contract must be ignored, not misattributed to the governor.
func TestEventsFromAnotherContractAreIgnored(t *testing.T) {
	store := newFakeGovernanceStore()
	h := newGovernanceHandler(t, store, newGovernorMeta(t))

	ev := governanceEvent(t, eventProposalCreated, proposalCreatedPayload(t, testChainID))
	ev.Contract = mustID(t, voteTokenHex)

	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(store.proposals) != 0 {
		t.Errorf("indexed an event from another contract: %+v", store.proposals)
	}
}
