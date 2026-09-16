package svm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mr-tron/base58"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

type fakeTx struct {
	slot     uint64
	failed   bool
	keys     []string
	inner    []compiledInstruction
	topLevel bool
}

type fakeNode struct {
	genesis        string
	firstAvailable uint64
	signatures     []signatureInfo
	txs            map[string]fakeTx
	accounts       map[string]accountInfo
	calls          map[string]int

	simulationErr  any
	simulationLogs []string
	sentTx         string
}

func (n *fakeNode) serve(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		n.calls[req.Method]++
		var result any
		switch req.Method {
		case "getGenesisHash":
			result = n.genesis
		case "getFirstAvailableBlock":
			result = n.firstAvailable
		case "getSignaturesForAddress":
			var cfg struct {
				Before string `json:"before"`
				Limit  int    `json:"limit"`
			}
			_ = json.Unmarshal(req.Params[1], &cfg)
			start := 0
			if cfg.Before != "" {
				for i, s := range n.signatures {
					if s.Signature == cfg.Before {
						start = i + 1
					}
				}
			}
			end := min(start+cfg.Limit, len(n.signatures))
			result = n.signatures[start:end]
		case "getTransaction":
			var sig string
			_ = json.Unmarshal(req.Params[0], &sig)
			tx := n.txs[sig]
			var errField any
			if tx.failed {
				errField = map[string]any{"InstructionError": []any{0, "Custom"}}
			}
			groups := []any{map[string]any{"index": 0, "instructions": tx.inner}}
			result = map[string]any{
				"slot": tx.slot, "blockTime": 1_700_000_000 + int64(tx.slot),
				"meta":        map[string]any{"err": errField, "innerInstructions": groups},
				"transaction": map[string]any{"signatures": []string{sig}, "message": map[string]any{"accountKeys": tx.keys}},
			}
		case "getAccountInfo":
			var addr string
			_ = json.Unmarshal(req.Params[0], &addr)
			if a, ok := n.accounts[addr]; ok {
				result = map[string]any{"value": a}
			} else {
				result = map[string]any{"value": nil}
			}
		case "getLatestBlockhash":
			result = map[string]any{"value": map[string]any{"blockhash": Encode(pbtypes.Identity{1})}}
		case "simulateTransaction":
			_ = json.Unmarshal(req.Params[0], &n.sentTx)
			result = map[string]any{"value": map[string]any{"err": n.simulationErr, "logs": n.simulationLogs}}
		case "sendTransaction":
			result = signature(77)
		case "getSignatureStatuses":
			result = map[string]any{"value": []any{map[string]any{"err": nil, "confirmationStatus": "finalized"}}}
		default:
			t.Errorf("unexpected method %s", req.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	}))
}

func signature(seed byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = seed
	}
	return base58.Encode(b)
}

type harness struct {
	node    *fakeNode
	client  *Client
	program pbtypes.Identity
	idl     *IDL
}

func newHarness(t *testing.T, chainID int64) *harness {
	t.Helper()
	idl := vaultIDL(t)
	node := &fakeNode{txs: map[string]fakeTx{}, accounts: map[string]accountInfo{}, calls: map[string]int{}}
	srv := node.serve(t)
	t.Cleanup(srv.Close)
	client, err := newClient(Options{RPCURL: srv.URL, ChainID: chainID, Programs: []Registration{{IDL: idl}}})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{node: node, client: client, program: idl.Program, idl: idl}
}

// eventTx is a transaction whose inner instructions carry a Deposited event for each amount.
func (h *harness) eventTx(t *testing.T, sig string, slot uint64, amounts ...uint64) {
	t.Helper()
	keys := []string{Encode(pbtypes.Identity{1}), Encode(h.program), Encode(eventAuthority(h.program))}
	var inner []compiledInstruction
	for _, a := range amounts {
		data := depositedData(t, h.idl, pbtypes.Identity{7}, TokenProgram, a, big.NewInt(int64(a)*1000))
		inner = append(inner, compiledInstruction{ProgramIDIndex: 1, Accounts: []int{2, 1}, Data: base58.Encode(data)})
	}
	h.node.txs[sig] = fakeTx{slot: slot, keys: keys, inner: inner}
}

func (h *harness) logs(t *testing.T, from, to uint64) ([]chain.Event, error) {
	t.Helper()
	return h.client.LogsInRange(context.Background(), from, to, []chain.Filter{{Contract: h.program, Names: []string{"Deposited"}}})
}

func TestEventsInRangeAreReturnedOldestFirst(t *testing.T) {
	h := newHarness(t, pbtypes.ChainIDSolanaLocalnet)
	h.eventTx(t, signature(3), 30, 3)
	h.eventTx(t, signature(2), 20, 2)
	h.eventTx(t, signature(1), 10, 1)
	h.eventTx(t, signature(9), 90, 9)
	h.node.signatures = []signatureInfo{{Signature: signature(9), Slot: 90}, {Signature: signature(3), Slot: 30}, {Signature: signature(2), Slot: 20}, {Signature: signature(1), Slot: 10}}

	events, err := h.logs(t, 15, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].BlockNumber != 20 || events[1].BlockNumber != 30 {
		t.Fatalf("events = %+v, want slots 20 then 30", events)
	}
	ev := events[0]
	if ev.TxHash != signature(2) || ev.Contract != h.program || ev.Name != "Deposited" || ev.ChainID != pbtypes.ChainIDSolanaLocalnet {
		t.Errorf("event = %+v", ev)
	}
	if ev.BlockTime != 1_700_000_020 {
		t.Errorf("block time = %d", ev.BlockTime)
	}
}

func TestSignaturesArePagedUntilTheRangeStart(t *testing.T) {
	h := newHarness(t, pbtypes.ChainIDSolanaLocalnet)
	for i := 0; i < signaturePageLimit+5; i++ {
		slot := uint64(2000 - i)
		h.node.signatures = append(h.node.signatures, signatureInfo{Signature: signature(byte(i%250) + 1), Slot: slot, Err: json.RawMessage(`{"x":1}`)})
	}
	sig := signature(251)
	h.eventTx(t, sig, 990, 5)
	h.node.signatures = append(h.node.signatures, signatureInfo{Signature: sig, Slot: 990})

	events, err := h.logs(t, 990, 990)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || h.node.calls["getSignaturesForAddress"] != 2 {
		t.Fatalf("events = %d, pages = %d", len(events), h.node.calls["getSignaturesForAddress"])
	}
}

// A failed transaction can still report the inner instructions that ran before it failed.
func TestFailedTransactionsAreNotIndexed(t *testing.T) {
	h := newHarness(t, pbtypes.ChainIDSolanaLocalnet)
	h.eventTx(t, signature(1), 10, 1)
	h.eventTx(t, signature(2), 10, 2)
	tx := h.node.txs[signature(2)]
	tx.failed = true
	h.node.txs[signature(2)] = tx
	h.eventTx(t, signature(3), 10, 3)
	h.node.signatures = []signatureInfo{
		{Signature: signature(3), Slot: 10, Err: json.RawMessage(`{"InstructionError":[0,"Custom"]}`)},
		{Signature: signature(2), Slot: 10},
		{Signature: signature(1), Slot: 10, Err: json.RawMessage(`null`)},
	}

	events, err := h.logs(t, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].TxHash != signature(1) {
		t.Fatalf("events = %+v, want only the successful transaction", events)
	}
}

func TestEventDataOutsideAnInnerProgramCallIsIgnoredOrRefused(t *testing.T) {
	t.Run("to another program", func(t *testing.T) {
		h := newHarness(t, pbtypes.ChainIDSolanaLocalnet)
		h.eventTx(t, signature(1), 10, 1)
		tx := h.node.txs[signature(1)]
		tx.keys[1] = Encode(TokenProgram)
		h.node.txs[signature(1)] = tx
		h.node.signatures = []signatureInfo{{Signature: signature(1), Slot: 10}}
		if events, err := h.logs(t, 10, 10); err != nil || len(events) != 0 {
			t.Fatalf("events = %v, err = %v", events, err)
		}
	})

	t.Run("without the program's event authority", func(t *testing.T) {
		h := newHarness(t, pbtypes.ChainIDSolanaLocalnet)
		h.eventTx(t, signature(1), 10, 1)
		tx := h.node.txs[signature(1)]
		tx.keys[2] = Encode(pbtypes.Identity{9})
		h.node.txs[signature(1)] = tx
		h.node.signatures = []signatureInfo{{Signature: signature(1), Slot: 10}}
		if _, err := h.logs(t, 10, 10); err == nil || !strings.Contains(err.Error(), "event authority") {
			t.Fatalf("err = %v", err)
		}
	})
}

// log_index is part of a row's identity, so it must not depend on which events a caller asked for.
func TestLogIndexIsStableAcrossFilters(t *testing.T) {
	h := newHarness(t, pbtypes.ChainIDSolanaLocalnet)
	h.eventTx(t, signature(1), 10, 1, 2)
	data := append(append([]byte{}, eventInstructionTag...), h.idl.events["PausedSet"].discriminator...)
	data = append(append(data, TokenProgram[:]...), 1)
	tx := h.node.txs[signature(1)]
	tx.inner = append([]compiledInstruction{{ProgramIDIndex: 1, Accounts: []int{2}, Data: base58.Encode(data)}}, tx.inner...)
	h.node.txs[signature(1)] = tx
	h.node.signatures = []signatureInfo{{Signature: signature(1), Slot: 10}}

	all, err := h.client.LogsInRange(context.Background(), 10, 10, []chain.Filter{{Contract: h.program}})
	if err != nil {
		t.Fatal(err)
	}
	deposits, err := h.logs(t, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || len(deposits) != 2 {
		t.Fatalf("all = %d, deposits = %d", len(all), len(deposits))
	}
	if deposits[0].LogIndex != 1 || deposits[1].LogIndex != 2 {
		t.Fatalf("deposit log indexes = %d, %d; want 1, 2", deposits[0].LogIndex, deposits[1].LogIndex)
	}
}

// A pruned node returns no signatures for old slots, which looks exactly like no activity.
func TestARangeTheNodeNoLongerHoldsIsRefused(t *testing.T) {
	h := newHarness(t, pbtypes.ChainIDSolanaLocalnet)
	h.node.firstAvailable = 500
	if _, err := h.logs(t, 499, 600); err == nil {
		t.Fatal("a pruned range was read as empty")
	}
	if _, err := h.logs(t, 500, 600); err != nil {
		t.Fatalf("available range refused: %v", err)
	}
}

func TestAnUnregisteredProgramIsRefused(t *testing.T) {
	h := newHarness(t, pbtypes.ChainIDSolanaLocalnet)
	_, err := h.client.LogsInRange(context.Background(), 1, 2, []chain.Filter{{Contract: TokenProgram}})
	if err == nil {
		t.Fatal("filter for an unregistered program accepted")
	}
}

func TestAClusterWithAPinnedGenesisMustMatchIt(t *testing.T) {
	node := &fakeNode{genesis: genesisHashes[pbtypes.ChainIDSolanaDevnet], calls: map[string]int{}}
	srv := node.serve(t)
	defer srv.Close()

	if _, err := New(context.Background(), Options{RPCURL: srv.URL, ChainID: pbtypes.ChainIDSolanaMainnet}); err == nil {
		t.Fatal("a devnet endpoint was accepted as mainnet")
	}
	if _, err := New(context.Background(), Options{RPCURL: srv.URL, ChainID: pbtypes.ChainIDSolanaDevnet}); err != nil {
		t.Fatalf("devnet refused: %v", err)
	}
	if _, err := New(context.Background(), Options{RPCURL: srv.URL, ChainID: pbtypes.ChainIDArbitrumOne}); err == nil {
		t.Fatal("an EVM chain id was accepted")
	}
}

func mintAccount(owner pbtypes.Identity, decimals byte, initialized bool) accountInfo {
	data := make([]byte, mintAccountLen)
	data[mintDecimalsOffset] = decimals
	if initialized {
		data[mintInitialized] = 1
	}
	return accountInfo{Owner: Encode(owner), Data: []string{base64.StdEncoding.EncodeToString(data), "base64"}}
}

func TestTokenMetadataReadsOnlyInitializedClassicMints(t *testing.T) {
	h := newHarness(t, pbtypes.ChainIDSolanaLocalnet)
	good, token2022, uninitialized := pbtypes.Identity{1}, pbtypes.Identity{2}, pbtypes.Identity{3}
	h.node.accounts[Encode(good)] = mintAccount(TokenProgram, 6, true)
	h.node.accounts[Encode(token2022)] = mintAccount(MustIdentity("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"), 6, true)
	h.node.accounts[Encode(uninitialized)] = mintAccount(TokenProgram, 6, false)

	meta, err := h.client.TokenMetadata(context.Background(), good)
	if err != nil || meta.Decimals != 6 {
		t.Fatalf("meta = %+v, err = %v", meta, err)
	}
	for name, id := range map[string]pbtypes.Identity{"token-2022": token2022, "uninitialized": uninitialized, "missing": {4}} {
		if _, err := h.client.TokenMetadata(context.Background(), id); err == nil {
			t.Errorf("%s mint accepted", name)
		}
	}
}

func vaultAccount(t *testing.T, idl *IDL, mint pbtypes.Identity, offset byte) []byte {
	t.Helper()
	data := make([]byte, 8+325)
	copy(data, idl.accounts["Vault"].discriminator)
	copy(data[8+3:], mint[:])
	data[8+197] = offset
	return data
}

func TestVaultLocatorReadsTheVaultAtItsDerivedAddress(t *testing.T) {
	h := newHarness(t, pbtypes.ChainIDSolanaLocalnet)
	mint := pbtypes.Identity{5}
	vault, _, _ := FindProgramAddress([][]byte{[]byte("vault"), mint[:]}, h.program)
	h.node.accounts[Encode(vault)] = accountInfo{Owner: Encode(h.program), Data: []string{base64.StdEncoding.EncodeToString(vaultAccount(t, h.idl, mint, 3)), "base64"}}

	locator := NewVaultLocator(h.client)
	got, asset, offset, err := locator.LocateVault(context.Background(), h.program, mint)
	if err != nil {
		t.Fatal(err)
	}
	if got != vault || asset != mint || offset != 3 {
		t.Fatalf("vault = %s, asset = %s, offset = %d", Encode(got), Encode(asset), offset)
	}

	h.node.accounts[Encode(vault)] = accountInfo{Owner: Encode(TokenProgram), Data: h.node.accounts[Encode(vault)].Data}
	if _, _, _, err := locator.LocateVault(context.Background(), h.program, mint); err == nil {
		t.Fatal("a vault account owned by another program was accepted")
	}
	if _, _, _, err := locator.LocateVault(context.Background(), h.program, pbtypes.Identity{6}); err == nil {
		t.Fatal("a missing vault was accepted")
	}
}
