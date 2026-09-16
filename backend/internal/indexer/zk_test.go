package indexer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	zkTreeHex     = "0x0165878a594ca255338adfa4d48449f69242eb8f"
	zkGateHex     = "0xa513e6e4b8f2a923d98304ec87f64353c4d5c853"
	zkVerifierHex = "0x2279b7a0a67db372996a5fab50d91eaa73d2ebe6"
)

type fakeZkStore struct {
	gates        []types.ZkGateMetadata
	commitments  []db.ZkCommitment
	actions      []db.ZkActionRow
	deregistered []string
	spent        []db.ZkPrivateAction
}

func (f *fakeZkStore) UpsertZkGate(_ context.Context, g types.ZkGateMetadata) error {
	f.gates = append(f.gates, g)
	return nil
}

func (f *fakeZkStore) InsertZkCommitment(_ context.Context, c db.ZkCommitment) error {
	f.commitments = append(f.commitments, c)
	return nil
}

func (f *fakeZkStore) UpsertZkAction(_ context.Context, a db.ZkActionRow) error {
	f.actions = append(f.actions, a)
	return nil
}

func (f *fakeZkStore) DeregisterZkAction(_ context.Context, _ int64, _, actionID string) error {
	f.deregistered = append(f.deregistered, actionID)
	return nil
}

func (f *fakeZkStore) InsertZkPrivateAction(_ context.Context, a db.ZkPrivateAction) error {
	f.spent = append(f.spent, a)
	return nil
}

type fakeZkGateMeta struct {
	tree     types.Identity
	verifier types.Identity
	err      error
	calls    int
}

func (f *fakeZkGateMeta) ZkGateMetadata(context.Context, types.Identity) (types.Identity, types.Identity, error) {
	f.calls++
	if f.err != nil {
		return types.Identity{}, types.Identity{}, f.err
	}
	return f.tree, f.verifier, nil
}

func newZkHandler(t *testing.T, store ZkStore, meta ZkGateMetadataReader) *ZkHandler {
	t.Helper()
	return &ZkHandler{
		tree:     mustID(t, zkTreeHex),
		gate:     mustID(t, zkGateHex),
		encode:   func(id types.Identity) string { return id.EVMHex() },
		gateMeta: meta,
		store:    store,
		chainID:  testChainID,
	}
}

func newZkGateMeta(t *testing.T) *fakeZkGateMeta {
	t.Helper()
	return &fakeZkGateMeta{tree: mustID(t, zkTreeHex), verifier: mustID(t, zkVerifierHex)}
}

func zkEvent(t *testing.T, contract, name string, payload map[string]any) chain.Event {
	t.Helper()
	return chain.Event{
		ChainID:     testChainID,
		BlockNumber: 120,
		BlockTime:   1735689600,
		TxHash:      "0xfeed",
		LogIndex:    4,
		Contract:    mustID(t, contract),
		Name:        name,
		Payload:     payload,
	}
}

// The tree is mirrored from logs and never asserted by an off-chain party — §2.2. The leaf index
// and the resulting root both come from the event, so the mirror can be checked against the chain
// leaf by leaf rather than only at the tip.
func TestCommitmentInsertedIsMirroredWithItsIndexAndRoot(t *testing.T) {
	store := &fakeZkStore{}
	h := newZkHandler(t, store, newZkGateMeta(t))

	ev := zkEvent(t, zkTreeHex, eventCommitmentInserted, map[string]any{
		"leafIndex":  mustBigInt(t, "7"),
		"commitment": [32]byte{0xaa, 0xbb},
		"root":       [32]byte{0xcc, 0xdd},
		"timestamp":  mustBigInt(t, "1735689600"),
	})
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if len(store.commitments) != 1 {
		t.Fatalf("commitments = %d, want 1", len(store.commitments))
	}
	c := store.commitments[0]
	if c.LeafIndex != 7 {
		t.Errorf("leaf index = %d, want 7", c.LeafIndex)
	}
	if c.RootAfter == "" || c.RootAfter == c.Commitment {
		t.Errorf("root and commitment were conflated: %+v", c)
	}
	if !c.InsertedAt.Equal(time.Unix(1735689600, 0).UTC()) {
		t.Errorf("inserted at = %s", c.InsertedAt)
	}
	if c.LogIndex != 4 {
		t.Errorf("log index = %d, want 4", c.LogIndex)
	}
}

// The privacy property, asserted rather than assumed: the indexed rows carry nothing that links a
// commitment to the action that spends it. A sender column on both would make that link a join.
func TestNoIndexedRowRecordsASender(t *testing.T) {
	store := &fakeZkStore{}
	h := newZkHandler(t, store, newZkGateMeta(t))
	ctx := context.Background()

	inserted := zkEvent(t, zkTreeHex, eventCommitmentInserted, map[string]any{
		"leafIndex":  mustBigInt(t, "0"),
		"commitment": [32]byte{0x11},
		"root":       [32]byte{0x22},
		"timestamp":  mustBigInt(t, "1735689600"),
	})
	spent := zkEvent(t, zkGateHex, eventPrivateActionExecuted, map[string]any{
		"nullifier": [32]byte{0x33},
		"actionId":  [32]byte{0x44},
		"root":      [32]byte{0x22},
		"chainId":   mustBigInt(t, "31337"),
	})

	if err := h.Handle(ctx, inserted); err != nil {
		t.Fatalf("handle insert: %v", err)
	}
	if err := h.Handle(ctx, spent); err != nil {
		t.Fatalf("handle spend: %v", err)
	}

	// The row types are the contract. If a sender field is ever added, this fails to compile rather
	// than silently weakening the anonymity the module exists to provide.
	var commitment db.ZkCommitment = store.commitments[0]
	var action db.ZkPrivateAction = store.spent[0]

	if commitment.TreeAddress == "" || action.GateAddress == "" {
		t.Fatal("rows were not attributed to their contracts")
	}
	if action.Root != commitment.RootAfter {
		t.Errorf("the spend's root %s does not match the tree's %s", action.Root, commitment.RootAfter)
	}
}

func TestPrivateActionIsIndexedWithItsDomain(t *testing.T) {
	store := &fakeZkStore{}
	h := newZkHandler(t, store, newZkGateMeta(t))

	ev := zkEvent(t, zkGateHex, eventPrivateActionExecuted, map[string]any{
		"nullifier": [32]byte{0xde, 0xad},
		"actionId":  [32]byte{0x07},
		"root":      [32]byte{0xbe, 0xef},
		"chainId":   mustBigInt(t, "31337"),
	})
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if len(store.spent) != 1 {
		t.Fatalf("private actions = %d, want 1", len(store.spent))
	}
	a := store.spent[0]
	if a.Nullifier == a.ActionID || a.ActionID == a.Root {
		t.Errorf("the domain fields were conflated: %+v", a)
	}
	if a.GateAddress != zkGateHex {
		t.Errorf("gate = %s", a.GateAddress)
	}
}

// The gate's wiring is recorded before any row with a foreign key to it.
func TestTheGateIsRecordedBeforeAnyRowThatReferencesIt(t *testing.T) {
	store := &fakeZkStore{}
	meta := newZkGateMeta(t)
	h := newZkHandler(t, store, meta)
	ctx := context.Background()

	ev := zkEvent(t, zkGateHex, eventActionRegistered, map[string]any{
		"actionId": [32]byte{0x07},
		"name":     "vault-membership",
	})
	if err := h.Handle(ctx, ev); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if len(store.gates) != 1 {
		t.Fatalf("gate not recorded: %+v", store.gates)
	}
	if store.gates[0].TreeAddress != zkTreeHex {
		t.Errorf("tree = %s, want %s", store.gates[0].TreeAddress, zkTreeHex)
	}
	if store.gates[0].VerifierAddress != zkVerifierHex {
		t.Errorf("verifier = %s", store.gates[0].VerifierAddress)
	}

	// Metadata that cannot change is not re-read.
	if err := h.Handle(ctx, ev); err != nil {
		t.Fatalf("second handle: %v", err)
	}
	if meta.calls != 1 {
		t.Errorf("gate metadata read %d times, want 1", meta.calls)
	}
}

// Indexing a spend against a gate whose tree is unknown would attribute it to the wrong commitment
// set, so it fails the batch instead.
func TestAPrivateActionIsNotIndexedWhenTheGateCannotBeRead(t *testing.T) {
	store := &fakeZkStore{}
	meta := newZkGateMeta(t)
	meta.err = errors.New("rpc down")
	h := newZkHandler(t, store, meta)

	ev := zkEvent(t, zkGateHex, eventPrivateActionExecuted, map[string]any{
		"nullifier": [32]byte{0x01},
		"actionId":  [32]byte{0x07},
		"root":      [32]byte{0x02},
		"chainId":   mustBigInt(t, "31337"),
	})
	if err := h.Handle(context.Background(), ev); err == nil {
		t.Fatal("expected the batch to fail")
	}
	if len(store.spent) != 0 {
		t.Errorf("a spend was attributed to an unknown tree: %+v", store.spent)
	}
}

func TestActionRegistrationRoundTrips(t *testing.T) {
	store := &fakeZkStore{}
	h := newZkHandler(t, store, newZkGateMeta(t))
	ctx := context.Background()

	registered := zkEvent(t, zkGateHex, eventActionRegistered, map[string]any{
		"actionId": [32]byte{0x07},
		"name":     "vault-membership",
	})
	deregistered := zkEvent(t, zkGateHex, eventActionDeregistered, map[string]any{
		"actionId": [32]byte{0x07},
	})

	if err := h.Handle(ctx, registered); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := h.Handle(ctx, deregistered); err != nil {
		t.Fatalf("deregister: %v", err)
	}

	if len(store.actions) != 1 || store.actions[0].Name != "vault-membership" {
		t.Fatalf("action not recorded: %+v", store.actions)
	}
	if len(store.deregistered) != 1 {
		t.Fatalf("deregistration not recorded: %+v", store.deregistered)
	}
}

// The two contracts share a handler, so an event from one must never be attributed to the other.
func TestEventsAreAttributedToTheRightContract(t *testing.T) {
	store := &fakeZkStore{}
	h := newZkHandler(t, store, newZkGateMeta(t))

	// A commitment event, but emitted by the gate's address.
	ev := zkEvent(t, zkGateHex, eventCommitmentInserted, map[string]any{
		"leafIndex":  mustBigInt(t, "0"),
		"commitment": [32]byte{0x11},
		"root":       [32]byte{0x22},
		"timestamp":  mustBigInt(t, "1735689600"),
	})
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(store.commitments) != 0 {
		t.Errorf("a gate event was indexed as a tree event: %+v", store.commitments)
	}
}

func TestMalformedZkEventsFail(t *testing.T) {
	store := &fakeZkStore{}
	h := newZkHandler(t, store, newZkGateMeta(t))

	ev := zkEvent(t, zkTreeHex, eventCommitmentInserted, map[string]any{
		"leafIndex":  mustBigInt(t, "0"),
		"commitment": [32]byte{0x11},
		// root missing
		"timestamp": mustBigInt(t, "1735689600"),
	})
	if err := h.Handle(context.Background(), ev); err == nil {
		t.Fatal("expected a missing field to fail the batch")
	}
	if len(store.commitments) != 0 {
		t.Errorf("a partial commitment was written: %+v", store.commitments)
	}
}
