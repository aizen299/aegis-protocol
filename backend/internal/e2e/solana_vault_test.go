//go:build e2e

package e2e

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mr-tron/base58"
	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/api"
	"github.com/aizen299/aegis-protocol/backend/internal/chain/evm"
	"github.com/aizen299/aegis-protocol/backend/internal/chain/svm"
	"github.com/aizen299/aegis-protocol/backend/internal/indexer"
	"github.com/aizen299/aegis-protocol/backend/internal/vault"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/solvault"
	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	solanaChainID    = pbtypes.ChainIDSolanaLocalnet
	solanaDepositRaw = uint64(1_500_000)
)

// eventTag is Anchor's prefix on emit_cpi! instruction data.
var eventTag = string([]byte{0xe4, 0x45, 0xa5, 0x2e, 0x51, 0xcb, 0x9a, 0x1d})

// §10.9: a deposit on each chain, indexed into one database and served under its own chain.
func TestASolanaDepositIsIndexedAndServedBesideAnArbitrumOne(t *testing.T) {
	s := setupStack(t)
	authority := requireSolana(t)
	ctx := context.Background()

	depositOnce(t, s)
	s.indexToHead(t)

	idl, err := svm.ParseIDL(solvault.IDL)
	if err != nil {
		t.Fatal(err)
	}
	alice := newSolanaKey(t)
	v, aliceTokens := setupSolanaVault(t, idl.Program, authority, alice, 3*solanaDepositRaw)

	depositSig := sendOK(t, alice, nil, "finalized", v.depositIx(alice, aliceTokens, solanaDepositRaw, 1))

	// A transaction whose first deposit succeeds and emits its event, and whose second fails its
	// slippage floor. The whole transaction is rolled back, so neither deposit happened.
	failedSig, failed := sendSolana(t, alice, nil, "finalized",
		v.depositIx(alice, aliceTokens, 500_000, 1),
		v.depositIx(alice, aliceTokens, 500_000, 1<<62))
	if !failed {
		t.Fatal("the transaction meant to fail succeeded")
	}
	requireReportedEvent(t, failedSig)

	client, err := svm.New(ctx, svm.Options{RPCURL: solanaRPC, ChainID: solanaChainID, Programs: []svm.Registration{{IDL: idl}}})
	if err != nil {
		t.Fatal(err)
	}
	lastSlot := transactionSlot(t, failedSig)
	indexSolana(t, s, client, idl.Program, "e2e-solana", lastSlot)

	aliceAddress := svm.Encode(alice.id())
	deposits, err := s.store.ListVaultDeposits(ctx, solanaChainID, aliceAddress, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(deposits) != 1 {
		t.Fatalf("indexed %d Solana deposits, want 1: the failed transaction's deposit must not appear", len(deposits))
	}
	got := deposits[0]
	if got.Amount.String() != "1500000" || got.Decimals != 6 {
		t.Errorf("amount = %s, decimals = %d", got.Amount.String(), got.Decimals)
	}
	if got.TxHash != depositSig {
		t.Errorf("tx hash = %s, want the 64-byte signature %s", got.TxHash, depositSig)
	}
	if got.VaultAddress != svm.Encode(v.vault) || got.AssetAddress != svm.Encode(v.mint) {
		t.Errorf("vault = %s, asset = %s", got.VaultAddress, got.AssetAddress)
	}

	srv := api.NewServer(s.cfg, zerolog.New(io.Discard), api.Deps{
		Store: s.store,
		Cache: s.cache,
		Chains: []api.ChainDeps{
			{ID: chainID, Codec: evm.Codec{}, Vault: vault.NewService(s.store, s.cache, zerolog.New(io.Discard), chainID)},
			{ID: solanaChainID, Codec: svm.Codec{}, Vault: vault.NewService(s.store, s.cache, zerolog.New(io.Discard), solanaChainID)},
		},
	}).Handler()

	status, body := serve(t, srv, "/v1/vault/positions/"+aliceAddress+"?chain=solana-localnet")
	if status != http.StatusOK || body["depositedTotal"] != "1500000" || body["decimals"] != float64(6) || body["shareDecimals"] != float64(9) {
		t.Errorf("solana position: status %d, body %v", status, body)
	}
	status, body = serve(t, srv, "/v1/vault/positions/"+aliceAddr+"?chain=anvil")
	if status != http.StatusOK || body["depositedTotal"] != depositRaw {
		t.Errorf("arbitrum position: status %d, body %v", status, body)
	}
	if status, _ := serve(t, srv, "/v1/vault/positions/"+aliceAddress+"?chain=anvil"); status != http.StatusBadRequest {
		t.Errorf("a Solana address was accepted on anvil: status %d", status)
	}
	if status, _ := serve(t, srv, "/v1/vault/positions/"+aliceAddress); status != http.StatusBadRequest {
		t.Errorf("a request naming no chain was answered: status %d", status)
	}

	// A second indexer over the same slots writes nothing new: (chain, signature, log index) is the
	// row's identity, and it must come out the same on replay.
	indexSolana(t, s, client, idl.Program, "e2e-solana-replay", lastSlot)
	replayed, err := s.store.ListVaultDeposits(ctx, solanaChainID, aliceAddress, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 1 {
		t.Fatalf("replay left %d deposits, want 1", len(replayed))
	}
}

func indexSolana(t *testing.T, s *stack, client *svm.Client, program pbtypes.Identity, service string, through uint64) {
	t.Helper()
	ctx := context.Background()
	idx := indexer.New(client, s.store, zerolog.New(io.Discard), indexer.Options{
		ServiceName: service,
		BatchSize:   1_000_000,
	}, indexer.NewVaultHandler(s.store, client, svm.NewVaultLocator(client), program))
	if err := idx.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for idx.Cursor() < through {
		if time.Now().After(deadline) {
			t.Fatalf("%s stalled at slot %d, want %d", service, idx.Cursor(), through)
		}
		if _, err := idx.Step(ctx); err != nil {
			t.Fatalf("%s step: %v", service, err)
		}
	}
}

// Without this, the failed-transaction check above would pass on a validator that simply omits
// inner instructions for failed transactions, and prove nothing.
func requireReportedEvent(t *testing.T, sig string) {
	t.Helper()
	var tx struct {
		Meta struct {
			InnerInstructions []struct {
				Instructions []struct {
					Data string `json:"data"`
				} `json:"instructions"`
			} `json:"innerInstructions"`
		} `json:"meta"`
	}
	if err := solanaCall(context.Background(), "getTransaction", []any{sig, map[string]any{"commitment": "finalized", "encoding": "json"}}, &tx); err != nil {
		t.Fatal(err)
	}
	for _, group := range tx.Meta.InnerInstructions {
		for _, ix := range group.Instructions {
			data, err := base58.Decode(ix.Data)
			if err == nil && strings.HasPrefix(string(data), eventTag) {
				return
			}
		}
	}
	t.Fatal("the failed transaction reports no event data, so its exclusion would not be tested")
}

func transactionSlot(t *testing.T, sig string) uint64 {
	t.Helper()
	var tx struct {
		Slot uint64 `json:"slot"`
	}
	if err := solanaCall(context.Background(), "getTransaction", []any{sig, map[string]any{"commitment": "finalized", "encoding": "json"}}, &tx); err != nil {
		t.Fatal(err)
	}
	return tx.Slot
}

func serve(t *testing.T, srv http.Handler, path string) (int, map[string]any) {
	t.Helper()
	return (&stack{server: srv}).get(t, path)
}
