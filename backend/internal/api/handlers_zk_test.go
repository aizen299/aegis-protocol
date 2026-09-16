package api

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	testTreeAddr  = "0x0165878a594ca255338adfa4d48449f69242eb8f"
	testGateAddr  = "0xa513e6e4b8f2a923d98304ec87f64353c4d5c853"
	testNullifier = "0x1111111111111111111111111111111111111111111111111111111111111111"
)

type stubZk struct {
	gate    types.ZkGateMetadata
	set     types.AnonymitySet
	leaves  []types.Commitment
	actions []types.ZkAction
	spends  []types.PrivateAction
	spent   bool
	err     error
	gateErr error

	gotTree      string
	gotGate      string
	gotNullifier string
	gotLimit     int
	gotOffset    int
}

func (s *stubZk) Gate(context.Context) (types.ZkGateMetadata, error) {
	if s.gateErr != nil {
		return types.ZkGateMetadata{}, s.gateErr
	}
	return s.gate, nil
}

func (s *stubZk) AnonymitySet(_ context.Context, tree string) (types.AnonymitySet, error) {
	s.gotTree = tree
	return s.set, s.err
}

func (s *stubZk) Commitments(_ context.Context, tree string, limit, offset int) ([]types.Commitment, error) {
	s.gotTree, s.gotLimit, s.gotOffset = tree, limit, offset
	return s.leaves, s.err
}

func (s *stubZk) PrivateActions(_ context.Context, gate string, limit, offset int) ([]types.PrivateAction, error) {
	s.gotGate, s.gotLimit, s.gotOffset = gate, limit, offset
	return s.spends, s.err
}

func (s *stubZk) NullifierSpent(_ context.Context, gate, nullifier string) (bool, error) {
	s.gotGate, s.gotNullifier = gate, nullifier
	return s.spent, s.err
}

func (s *stubZk) Actions(_ context.Context, gate string, limit, offset int) ([]types.ZkAction, error) {
	s.gotGate, s.gotLimit, s.gotOffset = gate, limit, offset
	return s.actions, s.err
}

func newZkServer(t *testing.T, stub *stubZk) http.Handler {
	t.Helper()

	cfg := &config.Config{}
	cfg.API.MaxPageSize = 100
	cfg.API.WriteTimeout = 5 * time.Second
	cfg.Chain.ChainID = testChainID

	h := &handlers{
		chains:      chainMap([]ChainDeps{{ID: testChainID, Codec: chainStub{}, Zk: stub}}),
		maxPageSize: cfg.API.MaxPageSize,
		log:         zerolog.New(io.Discard),
	}
	return routes(cfg, h, zerolog.New(io.Discard))
}

func wiredStub() *stubZk {
	return &stubZk{
		gate: types.ZkGateMetadata{
			ChainID:         testChainID,
			Address:         testGateAddr,
			TreeAddress:     testTreeAddr,
			VerifierAddress: "0x2279b7a0a67db372996a5fab50d91eaa73d2ebe6",
		},
	}
}

// --- the anonymity set ---

// / A membership proof is only as private as the number of members, and at one member it is not
// / private at all. The plan's risk register records this as unresolved and commits to surfacing it,
// / so it is an endpoint rather than something a caller must infer.
func TestTheAnonymitySetIsServed(t *testing.T) {
	stub := wiredStub()
	stub.set = types.AnonymitySet{
		ChainID:     testChainID,
		TreeAddress: testTreeAddr,
		LeafCount:   1,
		CurrentRoot: "0xabc",
	}

	code, body := get(t, newZkServer(t, stub), "/v1/zk/anonymity-set")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if body["leafCount"] != float64(1) {
		t.Errorf("leafCount = %v, want 1", body["leafCount"])
	}
	if stub.gotTree != testTreeAddr {
		t.Errorf("the set was read for %s, not the gate's tree", stub.gotTree)
	}
}

// --- commitments ---

// / Leaf order is what a Merkle path is built from; any other order describes a different tree.
func TestCommitmentsCarryTheirLeafIndex(t *testing.T) {
	stub := wiredStub()
	stub.leaves = []types.Commitment{
		{ChainID: testChainID, TreeAddress: testTreeAddr, LeafIndex: 0, Commitment: "0xaa", RootAfter: "0xbb"},
		{ChainID: testChainID, TreeAddress: testTreeAddr, LeafIndex: 1, Commitment: "0xcc", RootAfter: "0xdd"},
	}

	code, body := get(t, newZkServer(t, stub), "/v1/zk/commitments")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}

	items, ok := body["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items = %#v", body["items"])
	}
	first := items[0].(map[string]any)
	if first["leafIndex"] != float64(0) {
		t.Errorf("leafIndex = %v", first["leafIndex"])
	}
	if first["rootAfter"] == nil {
		t.Error("rootAfter is absent; the mirror cannot be checked leaf by leaf without it")
	}
}

// / The served shape must not carry an owner. A field linking a commitment to whoever inserted it
// / would hand away the anonymity the module exists to provide.
func TestNoServedZkRowCarriesASender(t *testing.T) {
	stub := wiredStub()
	stub.leaves = []types.Commitment{{ChainID: testChainID, TreeAddress: testTreeAddr, Commitment: "0xaa"}}
	stub.spends = []types.PrivateAction{
		{ChainID: testChainID, GateAddress: testGateAddr, Nullifier: testNullifier, ActionID: "0x07"},
	}

	srv := newZkServer(t, stub)
	for _, path := range []string{"/v1/zk/commitments", "/v1/zk/private-actions"} {
		_, body := get(t, srv, path)
		items, ok := body["items"].([]any)
		if !ok || len(items) == 0 {
			t.Fatalf("%s: items = %#v", path, body["items"])
		}

		row := items[0].(map[string]any)
		for _, forbidden := range []string{"sender", "from", "user", "owner", "address", "account"} {
			if _, present := row[forbidden]; present {
				t.Errorf("%s exposes %q, which links a commitment to a spender", path, forbidden)
			}
		}
	}
}

// --- nullifiers ---

func TestANullifierStatusIsServed(t *testing.T) {
	stub := wiredStub()
	stub.spent = true

	code, body := get(t, newZkServer(t, stub), "/v1/zk/nullifiers/"+testNullifier)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if body["spent"] != true {
		t.Errorf("spent = %v, want true", body["spent"])
	}
	if stub.gotNullifier != testNullifier {
		t.Errorf("nullifier = %s", stub.gotNullifier)
	}
	if stub.gotGate != testGateAddr {
		t.Errorf("the status was read for %s, not the gate", stub.gotGate)
	}
}

func TestAMalformedNullifierIsRejected(t *testing.T) {
	srv := newZkServer(t, wiredStub())

	for _, bad := range []string{
		"not-hex",
		"0x1234",
		"0x1111111111111111111111111111111111111111111111111111111111111111aa",
		"0x1111111111111111111111111111111111111111111111111111111111111GG",
	} {
		code, body := get(t, srv, "/v1/zk/nullifiers/"+bad)
		if code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", bad, code)
			continue
		}
		if body["code"] != "INVALID_NULLIFIER" {
			t.Errorf("%s: code = %v", bad, body["code"])
		}
	}
}

// / The canonical stored form is lowercase, so a checksummed nullifier would silently miss.
func TestAnUppercaseNullifierIsRejectedRatherThanMissing(t *testing.T) {
	upper := "0x1111111111111111111111111111111111111111111111111111111111111AAA"

	code, _ := get(t, newZkServer(t, wiredStub()), "/v1/zk/nullifiers/"+upper)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

// --- error mapping ---

func TestAMissingGateIsNotFound(t *testing.T) {
	stub := wiredStub()
	stub.gateErr = db.ErrNotFound

	srv := newZkServer(t, stub)
	for _, path := range []string{
		"/v1/zk/gate",
		"/v1/zk/anonymity-set",
		"/v1/zk/commitments",
		"/v1/zk/private-actions",
		"/v1/zk/actions",
		"/v1/zk/nullifiers/" + testNullifier,
	} {
		if code, _ := get(t, srv, path); code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, code)
		}
	}
}

func TestZkPaginationIsClampedAndValidated(t *testing.T) {
	stub := wiredStub()
	srv := newZkServer(t, stub)

	if code, _ := get(t, srv, "/v1/zk/commitments?limit=99999"); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if stub.gotLimit != 100 {
		t.Errorf("limit = %d, want it clamped to 100", stub.gotLimit)
	}

	for _, bad := range []string{"limit=0", "limit=-1", "limit=abc", "offset=-1"} {
		code, body := get(t, srv, "/v1/zk/commitments?"+bad)
		if code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", bad, code)
			continue
		}
		if body["code"] != "INVALID_PAGINATION" {
			t.Errorf("%s: code = %v", bad, body["code"])
		}
	}
}

func TestActionsAreServed(t *testing.T) {
	stub := wiredStub()
	stub.actions = []types.ZkAction{
		{ChainID: testChainID, GateAddress: testGateAddr, ActionID: "0x07", Name: "vault-membership", Registered: true},
	}

	code, body := get(t, newZkServer(t, stub), "/v1/zk/actions")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v", body["items"])
	}
	if items[0].(map[string]any)["registered"] != true {
		t.Errorf("registered = %v", items[0].(map[string]any)["registered"])
	}
}
