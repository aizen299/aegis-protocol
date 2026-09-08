//go:build e2e

package e2e

import (
	"context"
	"testing"
)

func TestWithdrawFlowNetsAgainstDeposit(t *testing.T) {
	s := setupStack(t)
	ctx := context.Background()

	depositOnce(t, s)
	send(t, aliceKey, s.deployment.VaultProxy, "withdraw(uint256,address)", withdrawShares, aliceAddr)
	s.indexToHead(t)

	pos, err := s.store.VaultPosition(ctx, chainID, aliceAddr)
	if err != nil {
		t.Fatalf("position: %v", err)
	}

	if pos.DepositedTotal.String() != depositRaw {
		t.Errorf("depositedTotal = %s, want %s", pos.DepositedTotal.String(), depositRaw)
	}
	// Half the shares were burned, so half the deposit comes back.
	if pos.WithdrawnTotal.String() != "750000" {
		t.Errorf("withdrawnTotal = %s, want 750000", pos.WithdrawnTotal.String())
	}
	if pos.Shares.String() != withdrawShares {
		t.Errorf("remaining shares = %s, want %s", pos.Shares.String(), withdrawShares)
	}
	if pos.Decimals != 6 {
		t.Errorf("decimals = %d, want 6", pos.Decimals)
	}
}

// The indexer's idempotency claim rests on ON CONFLICT DO NOTHING. Replaying a range is what
// happens after every crash, so it has to be a genuine no-op against a real database.
func TestReindexingIsIdempotent(t *testing.T) {
	s := setupStack(t)
	ctx := context.Background()

	depositOnce(t, s)
	s.indexToHead(t)

	before, err := s.store.ListVaultDeposits(ctx, chainID, aliceAddr, 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("expected 1 deposit before replay, got %d", len(before))
	}

	// Rewind the cursor and replay the same range, exactly as a crashed process would.
	if err := s.store.SaveCursor(ctx, "e2e", chainID, 0); err != nil {
		t.Fatalf("rewind cursor: %v", err)
	}
	s.replayFromGenesis(t)

	after, err := s.store.ListVaultDeposits(ctx, chainID, aliceAddr, 100, 0)
	if err != nil {
		t.Fatalf("list after replay: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("replay produced %d rows, want 1 — the conflict target is not catching duplicates", len(after))
	}
}

// The reorg guard is the reason nothing above head - ConfirmationDepth is written. An event in the
// newest block must stay invisible until it is confirmed.
func TestUnconfirmedBlocksAreNotIndexed(t *testing.T) {
	s := setupStack(t)
	ctx := context.Background()

	depositOnce(t, s)

	// No mining: the deposit sits in the head block, inside the confirmation window.
	for {
		advanced, err := s.indexer.Step(ctx)
		if err != nil {
			t.Fatalf("step: %v", err)
		}
		if !advanced {
			break
		}
	}

	head := headBlock(t)
	if s.indexer.Cursor() >= head {
		t.Fatalf("cursor %d reached head %d; the confirmation window was not respected",
			s.indexer.Cursor(), head)
	}

	mineBlocks(t, 3)
	s.indexToHead(t)

	rows, err := s.store.ListVaultDeposits(ctx, chainID, aliceAddr, 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("after confirmation got %d rows, want 1", len(rows))
	}
}

func TestTVLReflectsIndexedFlows(t *testing.T) {
	s := setupStack(t)
	ctx := context.Background()

	depositOnce(t, s)
	send(t, aliceKey, s.deployment.VaultProxy, "withdraw(uint256,address)", withdrawShares, aliceAddr)
	s.indexToHead(t)

	tvl, decimals, asset, err := s.store.VaultTVL(ctx, chainID, s.deployment.VaultProxy)
	if err != nil {
		t.Fatalf("tvl: %v", err)
	}

	if tvl.String() != "750000" {
		t.Errorf("tvl = %s, want 750000 (1.5 in, 0.75 out, six decimals)", tvl.String())
	}
	if decimals != 6 {
		t.Errorf("decimals = %d, want 6", decimals)
	}
	if asset != s.deployment.Asset {
		t.Errorf("asset = %s, want %s", asset, s.deployment.Asset)
	}
}
