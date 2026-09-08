package indexer

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	vaultHex = "0x5fbdb2315678afecb367f032d93f642f64180aa3"
	userHex  = "0x70997970c51812dc3a010c7d01b50e0d17dc79c8"
	assetHex = "0xe7f1725e7734ce288f8367e1bb143e90bb3f0512"
)

type fakeVaultStore struct {
	assets      []types.AssetMetadata
	deposits    []db.VaultEvent
	withdrawals []db.VaultEvent
	assetErr    error
}

func (f *fakeVaultStore) UpsertAsset(_ context.Context, a types.AssetMetadata) error {
	if f.assetErr != nil {
		return f.assetErr
	}
	f.assets = append(f.assets, a)
	return nil
}

func (f *fakeVaultStore) InsertVaultDeposit(_ context.Context, e db.VaultEvent) error {
	f.deposits = append(f.deposits, e)
	return nil
}

func (f *fakeVaultStore) InsertVaultWithdrawal(_ context.Context, e db.VaultEvent) error {
	f.withdrawals = append(f.withdrawals, e)
	return nil
}

type fakeResolver struct {
	meta  chain.TokenMeta
	err   error
	calls int
}

func (f *fakeResolver) TokenMetadata(context.Context, types.Identity) (chain.TokenMeta, error) {
	f.calls++
	if f.err != nil {
		return chain.TokenMeta{}, f.err
	}
	return f.meta, nil
}

func mustID(t *testing.T, hex string) types.Identity {
	t.Helper()
	id, err := types.IdentityFromEVMHex(hex)
	if err != nil {
		t.Fatalf("parse %s: %v", hex, err)
	}
	return id
}

func mustBigInt(t *testing.T, s string) *big.Int {
	t.Helper()
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("bad big.Int literal: %s", s)
	}
	return n
}

func newVaultHandler(t *testing.T, store VaultStore, resolver AssetResolver) *VaultHandler {
	t.Helper()
	return &VaultHandler{
		vault:    mustID(t, vaultHex),
		encode:   func(id types.Identity) string { return id.EVMHex() },
		resolver: resolver,
		store:    store,
		chainID:  testChainID,
		assets:   make(map[types.Identity]types.AssetMetadata),
	}
}

func vaultEvent(t *testing.T, name string, amount, shares string) chain.Event {
	t.Helper()
	return chain.Event{
		ChainID:     testChainID,
		BlockNumber: 42,
		BlockTime:   1735689600,
		TxHash:      [32]byte{0xde, 0xad, 0xbe, 0xef},
		LogIndex:    3,
		Contract:    mustID(t, vaultHex),
		Name:        name,
		Payload: map[string]any{
			"user":   mustID(t, userHex),
			"asset":  mustID(t, assetHex),
			"amount": mustBigInt(t, amount),
			"shares": mustBigInt(t, shares),
		},
	}
}

// Raw base units are stored exactly as emitted. Scaling on ingest would bake one token's decimals
// into every row.
func TestVaultRowPreservesRawAmounts(t *testing.T) {
	store := &fakeVaultStore{}
	resolver := &fakeResolver{meta: chain.TokenMeta{Decimals: 18, Symbol: "mUSD"}}
	h := newVaultHandler(t, store, resolver)

	const rawAmount = "1500000000000000000"
	const rawShares = "1500000000000000000000"

	if err := h.Handle(context.Background(), vaultEvent(t, eventDeposited, rawAmount, rawShares)); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if len(store.deposits) != 1 {
		t.Fatalf("want 1 deposit, got %d", len(store.deposits))
	}
	row := store.deposits[0]

	if row.Amount.String() != rawAmount {
		t.Errorf("amount = %s, want %s unmodified", row.Amount.String(), rawAmount)
	}
	if row.Shares.String() != rawShares {
		t.Errorf("shares = %s, want %s unmodified", row.Shares.String(), rawShares)
	}
	if row.LogIndex != 3 {
		t.Errorf("log_index = %d, want 3", row.LogIndex)
	}
	if row.At.Unix() != 1735689600 {
		t.Errorf("timestamp = %d, want the block time", row.At.Unix())
	}
}

// A six-decimal asset is the case the previous hardcoded scale got wrong by twelve orders of
// magnitude. The row must be byte-identical to what the chain emitted either way.
func TestVaultRowIsIndependentOfAssetDecimals(t *testing.T) {
	const rawAmount = "1500000" // 1.5 units of a 6-decimal token

	for _, decimals := range []uint8{6, 8, 18} {
		store := &fakeVaultStore{}
		h := newVaultHandler(t, store, &fakeResolver{meta: chain.TokenMeta{Decimals: decimals}})

		if err := h.Handle(context.Background(), vaultEvent(t, eventDeposited, rawAmount, "1500000000")); err != nil {
			t.Fatalf("decimals=%d handle: %v", decimals, err)
		}
		if got := store.deposits[0].Amount.String(); got != rawAmount {
			t.Errorf("decimals=%d: amount = %s, want %s", decimals, got, rawAmount)
		}
		if store.assets[0].Decimals != decimals {
			t.Errorf("decimals=%d: recorded %d", decimals, store.assets[0].Decimals)
		}
	}
}

func TestVaultHandlerRecordsAssetBeforeEvent(t *testing.T) {
	store := &fakeVaultStore{}
	h := newVaultHandler(t, store, &fakeResolver{meta: chain.TokenMeta{Decimals: 6, Symbol: "USDC", Name: "USD Coin"}})

	if err := h.Handle(context.Background(), vaultEvent(t, eventDeposited, "1000000", "1000000000")); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if len(store.assets) != 1 {
		t.Fatalf("want the asset recorded, got %d rows", len(store.assets))
	}
	asset := store.assets[0]
	if asset.Address != assetHex || asset.Decimals != 6 || asset.Symbol != "USDC" {
		t.Fatalf("asset row = %+v", asset)
	}
}

func TestVaultHandlerResolvesEachAssetOnce(t *testing.T) {
	store := &fakeVaultStore{}
	resolver := &fakeResolver{meta: chain.TokenMeta{Decimals: 18}}
	h := newVaultHandler(t, store, resolver)

	for range 3 {
		if err := h.Handle(context.Background(), vaultEvent(t, eventDeposited, "1", "1000")); err != nil {
			t.Fatalf("handle: %v", err)
		}
	}

	if resolver.calls != 1 {
		t.Fatalf("resolved metadata %d times, want 1 — the result is cached per asset", resolver.calls)
	}
}

// Guessing a scale is the bug this design removes, so an unreadable token must fail the batch.
func TestVaultHandlerFailsWhenDecimalsUnavailable(t *testing.T) {
	store := &fakeVaultStore{}
	h := newVaultHandler(t, store, &fakeResolver{err: errors.New("execution reverted")})

	err := h.Handle(context.Background(), vaultEvent(t, eventDeposited, "1", "1000"))
	if err == nil {
		t.Fatal("expected an error when decimals() cannot be read")
	}
	if len(store.deposits) != 0 {
		t.Fatal("a row was written without known decimals")
	}
}

func TestVaultHandlerRoutesWithdrawn(t *testing.T) {
	store := &fakeVaultStore{}
	h := newVaultHandler(t, store, &fakeResolver{meta: chain.TokenMeta{Decimals: 18}})

	if err := h.Handle(context.Background(), vaultEvent(t, eventWithdrawn, "5", "5000")); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(store.withdrawals) != 1 || len(store.deposits) != 0 {
		t.Fatalf("deposits=%d withdrawals=%d", len(store.deposits), len(store.withdrawals))
	}
}

func TestVaultRowTxHashIsLowercaseHex(t *testing.T) {
	store := &fakeVaultStore{}
	h := newVaultHandler(t, store, &fakeResolver{meta: chain.TokenMeta{Decimals: 18}})

	if err := h.Handle(context.Background(), vaultEvent(t, eventDeposited, "1", "1000")); err != nil {
		t.Fatalf("handle: %v", err)
	}

	want := "0xdeadbeef" + "00000000000000000000000000000000000000000000000000000000"
	if got := store.deposits[0].TxHash; got != want {
		t.Fatalf("tx_hash = %s, want %s", got, want)
	}
}

func TestVaultRowRejectsMissingField(t *testing.T) {
	store := &fakeVaultStore{}
	h := newVaultHandler(t, store, &fakeResolver{meta: chain.TokenMeta{Decimals: 18}})

	ev := vaultEvent(t, eventDeposited, "1", "1000")
	delete(ev.Payload, "shares")

	if err := h.Handle(context.Background(), ev); err == nil {
		t.Fatal("expected an error for a payload missing 'shares'")
	}
}

func TestVaultRowRejectsWrongFieldType(t *testing.T) {
	store := &fakeVaultStore{}
	h := newVaultHandler(t, store, &fakeResolver{meta: chain.TokenMeta{Decimals: 18}})

	ev := vaultEvent(t, eventDeposited, "1", "1000")
	ev.Payload["amount"] = "1500000000000000000"

	if err := h.Handle(context.Background(), ev); err == nil {
		t.Fatal("expected an error when 'amount' is not a *big.Int")
	}
}

func TestVaultHandlerFiltersToItsOwnEvents(t *testing.T) {
	h := newVaultHandler(t, &fakeVaultStore{}, &fakeResolver{})

	filters := h.Filters()
	if len(filters) != 1 {
		t.Fatalf("want one filter, got %d", len(filters))
	}
	if len(filters[0].Names) != 2 {
		t.Fatalf("want Deposited and Withdrawn, got %v", filters[0].Names)
	}
}
