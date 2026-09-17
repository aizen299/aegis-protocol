package indexer

import (
	"context"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

type closedRecord struct {
	emitterChain uint16
	sequence     string
	status       string
	txHash       string
	by           string
	at           time.Time
}

type fakeReceiverStore struct {
	received []db.RemoteActionReceived
	closed   []closedRecord
}

func (f *fakeReceiverStore) InsertRemoteAction(_ context.Context, a db.RemoteActionReceived) error {
	f.received = append(f.received, a)
	return nil
}

func (f *fakeReceiverStore) CloseRemoteAction(_ context.Context, _ int64, emitterChain uint16, sequence types.Raw, status string, at time.Time, txHash, by string) error {
	f.closed = append(f.closed, closedRecord{emitterChain, sequence.String(), status, txHash, by, at})
	return nil
}

var (
	receiverID = types.Identity{31: 0x77}
	targetID   = types.Identity{31: 0x88}
	executorID = types.Identity{31: 0x99}
)

func receiverEvent(name string, payload map[string]any) chain.Event {
	return chain.Event{ChainID: testChainID, BlockTime: 1_700_000_000, TxHash: "sig", Contract: receiverID, Name: name, Payload: payload}
}

func receivedPayload() map[string]any {
	var declared [32]byte
	declared[31] = 5
	return map[string]any{
		"emitterChain":  big.NewInt(23),
		"sequence":      big.NewInt(9),
		"sourceChainId": big.NewInt(31337),
		"operationId":   big.NewInt(4),
		"target":        targetID,
		"declaredValue": declared,
		"accountsHash":  [32]byte{1},
		"executableAt":  big.NewInt(1_700_000_060),
	}
}

func TestReceivedMessageBecomesAPendingRemoteAction(t *testing.T) {
	store := &fakeReceiverStore{}
	h := NewGovernanceReceiverHandler(store, &fakeClient{}, receiverID)
	if err := h.Handle(context.Background(), receiverEvent(eventMessageReceived, receivedPayload())); err != nil {
		t.Fatal(err)
	}
	if len(store.received) != 1 {
		t.Fatalf("recorded %d, want 1", len(store.received))
	}
	got := store.received[0]
	if got.EmitterChain != 23 || got.Sequence.String() != "9" || got.SourceChainID != 31337 || got.OperationID.String() != "4" {
		t.Errorf("key or correlation wrong: %+v", got)
	}
	if got.DeclaredValue.String() != "5" || got.AccountsHash != [32]byte{1} || got.Target != targetID.EVMHex() {
		t.Errorf("message fields wrong: %+v", got)
	}
	if !got.ExecutableAt.Equal(time.Unix(1_700_000_060, 0)) || !got.ReceivedAt.Equal(time.Unix(1_700_000_000, 0)) {
		t.Errorf("times: executable %v, received %v", got.ExecutableAt, got.ReceivedAt)
	}
	if got.Receiver != receiverID.EVMHex() || got.TxHash != "sig" {
		t.Errorf("receiver %s, tx %s", got.Receiver, got.TxHash)
	}
}

func TestExecutedAndCancelledCloseTheAction(t *testing.T) {
	store := &fakeReceiverStore{}
	h := NewGovernanceReceiverHandler(store, &fakeClient{}, receiverID)
	key := map[string]any{"emitterChain": big.NewInt(23), "sequence": big.NewInt(9), "sourceChainId": big.NewInt(31337), "operationId": big.NewInt(4)}
	executed := map[string]any{"executor": executorID}
	cancelled := map[string]any{"by": executorID}
	for k, v := range key {
		executed[k], cancelled[k] = v, v
	}
	if err := h.Handle(context.Background(), receiverEvent(eventMessageExecuted, executed)); err != nil {
		t.Fatal(err)
	}
	if err := h.Handle(context.Background(), receiverEvent(eventMessageCancelled, cancelled)); err != nil {
		t.Fatal(err)
	}
	want := []string{types.RemoteActionExecuted, types.RemoteActionCancelled}
	if len(store.closed) != 2 {
		t.Fatalf("closed %d, want 2", len(store.closed))
	}
	for i, c := range store.closed {
		if c.status != want[i] || c.emitterChain != 23 || c.sequence != "9" || c.by != executorID.EVMHex() || c.txHash != "sig" {
			t.Errorf("close %d: %+v", i, c)
		}
	}
}

func TestReceiverHandlerIgnoresOtherContractsAndEvents(t *testing.T) {
	store := &fakeReceiverStore{}
	h := NewGovernanceReceiverHandler(store, &fakeClient{}, receiverID)
	other := receiverEvent(eventMessageReceived, receivedPayload())
	other.Contract = targetID
	if err := h.Handle(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if err := h.Handle(context.Background(), receiverEvent("PausedSet", map[string]any{})); err != nil {
		t.Fatal(err)
	}
	if len(store.received)+len(store.closed) != 0 {
		t.Error("recorded an event it does not own")
	}
}

func TestReceiverHandlerRefusesMalformedEvents(t *testing.T) {
	h := NewGovernanceReceiverHandler(&fakeReceiverStore{}, &fakeClient{}, receiverID)
	cases := map[string]func(map[string]any){
		"emitter chain over u16": func(p map[string]any) { p["emitterChain"] = big.NewInt(1 << 16) },
		"source chain over i64":  func(p map[string]any) { p["sourceChainId"] = new(big.Int).Lsh(big.NewInt(1), 63) },
		"missing target":         func(p map[string]any) { delete(p, "target") },
		"hash of wrong type":     func(p map[string]any) { p["accountsHash"] = []byte{1} },
	}
	for name, mutate := range cases {
		p := receivedPayload()
		mutate(p)
		err := h.Handle(context.Background(), receiverEvent(eventMessageReceived, p))
		if err == nil || !strings.Contains(err.Error(), eventMessageReceived) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
