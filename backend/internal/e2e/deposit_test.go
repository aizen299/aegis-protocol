//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"
)

// The whole point of the E2E: a six-decimal amount must survive the chain, the log decoder, the
// indexer, Postgres, and the API without being rescaled. A hardcoded 18 anywhere in that path
// shows up here as a factor of 10^12.
func TestDepositFlowPreservesRawSixDecimalAmount(t *testing.T) {
	s := setupStack(t)
	ctx := context.Background()

	send(t, deployerKey, s.deployment.Asset, "transfer(address,uint256)", aliceAddr, depositRaw)
	send(t, aliceKey, s.deployment.Asset, "approve(address,uint256)", s.deployment.VaultProxy, depositRaw)
	send(t, aliceKey, s.deployment.VaultProxy, "deposit(uint256,address)", depositRaw, aliceAddr)

	s.indexToHead(t)

	deposits, err := s.store.ListVaultDeposits(ctx, chainID, aliceAddr, 10, 0)
	if err != nil {
		t.Fatalf("list deposits: %v", err)
	}
	if len(deposits) != 1 {
		t.Fatalf("indexed %d deposits, want 1 — the event never reached Postgres", len(deposits))
	}

	got := deposits[0]
	if got.Amount.String() != depositRaw {
		t.Errorf("amount = %s, want %s\nA mismatch by 10^12 means something rescaled a 6-decimal token as 18.",
			got.Amount.String(), depositRaw)
	}
	if got.Decimals != 6 {
		t.Errorf("decimals = %d, want 6 — resolved from the token, not assumed", got.Decimals)
	}
	if got.AssetAddress != s.deployment.Asset {
		t.Errorf("asset = %s, want %s", got.AssetAddress, s.deployment.Asset)
	}
	if got.VaultAddress != s.deployment.VaultProxy {
		t.Errorf("vault = %s, want %s", got.VaultAddress, s.deployment.VaultProxy)
	}
	if got.BlockNumber == 0 || got.DepositedAt.IsZero() {
		t.Errorf("block %d / time %v — provenance was not decoded", got.BlockNumber, got.DepositedAt)
	}
}

// The indexer resolves decimals from the token itself. If TokenMetadata is broken, the foreign key
// makes the insert fail rather than storing an uninterpretable amount.
func TestAssetMetadataIsResolvedFromChain(t *testing.T) {
	s := setupStack(t)
	ctx := context.Background()

	depositOnce(t, s)
	s.indexToHead(t)

	meta, found, err := s.store.Asset(ctx, chainID, s.deployment.Asset)
	if err != nil {
		t.Fatalf("asset lookup: %v", err)
	}
	if !found {
		t.Fatal("no assets row — the indexer must record metadata before the event that needs it")
	}
	if meta.Decimals != 6 {
		t.Errorf("decimals = %d, want 6", meta.Decimals)
	}
	if meta.Symbol != "tUSD" {
		t.Errorf("symbol = %q, want tUSD — symbol() was not read", meta.Symbol)
	}
}

// The API is the last place a raw amount can be mangled: JSON numbers are float64.
func TestAPIServesRawAmountAndDecimals(t *testing.T) {
	s := setupStack(t)

	depositOnce(t, s)
	s.indexToHead(t)

	status, body := s.get(t, "/v1/vault/positions/"+aliceAddr)
	if status != http.StatusOK {
		t.Fatalf("status %d, body %v", status, body)
	}

	if got := body["depositedTotal"]; got != depositRaw {
		t.Errorf("depositedTotal = %v (%T), want the string %q\nA float64 here means a uint256 can silently lose precision.",
			got, got, depositRaw)
	}
	if got := body["decimals"]; got != float64(6) {
		t.Errorf("decimals = %v, want 6", got)
	}
	// Served again now that the indexer reads the offset from the contract. Six asset decimals
	// plus a three-place virtual-shares offset. A hardcoded zero here is the exact bug the first
	// run of this suite caught.
	if got := body["shareDecimals"]; got != float64(9) {
		t.Errorf("shareDecimals = %v, want 9 (6 asset decimals + a 3-place offset), resolved not assumed", got)
	}
}

// A checksummed address is what a wallet, explorer, or deploy artifact hands you. It must reach the
// same row as the lowercase form the indexer stored.
func TestAPIAcceptsChecksummedAddress(t *testing.T) {
	s := setupStack(t)

	depositOnce(t, s)
	s.indexToHead(t)

	const checksummed = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"

	status, body := s.get(t, "/v1/vault/positions/"+checksummed)
	if status != http.StatusOK {
		t.Fatalf("status %d, body %v", status, body)
	}
	if got := body["depositedTotal"]; got != depositRaw {
		t.Errorf("depositedTotal = %v, want %q — a checksummed address must normalise to the stored form",
			got, depositRaw)
	}
	if got := body["user"]; got != aliceAddr {
		t.Errorf("user = %v, want the canonical lowercase %q", got, aliceAddr)
	}
}

func TestAPIRejectsMalformedAddress(t *testing.T) {
	s := setupStack(t)

	status, body := s.get(t, "/v1/vault/positions/not-an-address")
	if status != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body %v", status, body)
	}
	if body["code"] != "INVALID_ADDRESS" {
		t.Errorf("code = %v, want INVALID_ADDRESS", body["code"])
	}
}

func depositOnce(t *testing.T, s *stack) {
	t.Helper()
	send(t, deployerKey, s.deployment.Asset, "transfer(address,uint256)", aliceAddr, depositRaw)
	send(t, aliceKey, s.deployment.Asset, "approve(address,uint256)", s.deployment.VaultProxy, depositRaw)
	send(t, aliceKey, s.deployment.VaultProxy, "deposit(uint256,address)", depositRaw, aliceAddr)
}
