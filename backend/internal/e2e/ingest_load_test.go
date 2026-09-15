//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The ingest profile from docs/v1.0-production-plan.md §2.5.
//
// A load test that hammers the API measures Postgres and the read path. The component most likely
// to fall behind under real conditions is the indexer, and its load is set by chain activity rather
// than by callers — so nothing a request generator does can exercise it. This is the profile with a
// real chance of failing, and until now nothing tried: the rest of the end-to-end suite indexes
// tens of events, not thousands.
const (
	ingestCommitments = 400
	ingestDeadline    = 3 * time.Minute
)

func TestIngestKeepsUpWithADenseBlockRange(t *testing.T) {
	requireDeps(t)
	d := deployZk(t)
	s := setupZkStack(t, d)

	// One transaction per commitment, so this is a dense range of blocks each carrying an event —
	// the shape that makes an indexer fall behind, rather than one block with a large batch.
	start := time.Now()
	for i := 0; i < ingestCommitments; i++ {
		secret := fmt.Sprintf("%d", 1_000_000+i)
		value := strings.Fields(call(t, d.PoseidonT2, "poseidon(uint256[1])(uint256)", "["+secret+"]"))[0]
		send(t, deployerKey, d.CommitmentTree, "insert(bytes32)", toBytes32(t, value))
	}
	produced := time.Since(start)

	// The indexer will not process the tip: it stops at head minus the confirmation depth, which is
	// the reorg guard doing its job. Without mining past the last insert the final event is
	// legitimately pending, and the run reads as one lost event.
	mineBlocks(t, 3)

	// Catch-up is measured separately from production: the interesting number is how long the
	// indexer needs once the events already exist, not how fast a test can create them.
	caughtUp := time.Now()
	deadline := time.Now().Add(ingestDeadline)
	steps := 0

	for {
		if time.Now().After(deadline) {
			indexed := zkQueryInt(t, s, `SELECT count(*) FROM zk_commitments WHERE chain_id = $1`, chainID)
			t.Fatalf("indexer did not catch up within %v: %d of %d events after %d steps",
				ingestDeadline, indexed, ingestCommitments, steps)
		}

		advanced, err := s.indexer.Step(context.Background())
		steps++
		if err != nil {
			// §2.5's criterion is that no batch is retried. An error here is a retry.
			t.Fatalf("a batch failed and would be retried: %v", err)
		}
		if !advanced {
			break
		}
	}
	catchUp := time.Since(caughtUp)

	indexed := zkQueryInt(t, s, `SELECT count(*) FROM zk_commitments WHERE chain_id = $1`, chainID)
	if indexed != ingestCommitments {
		t.Fatalf("indexed %d of %d commitments", indexed, ingestCommitments)
	}

	// The leaf indices must be contiguous from zero. A gap means the mirror cannot build a Merkle
	// path, which is a silent failure until someone tries to prove membership.
	maxIndex := zkQueryInt(t, s,
		`SELECT COALESCE(MAX(leaf_index), -1) FROM zk_commitments WHERE chain_id = $1`, chainID)
	if maxIndex != ingestCommitments-1 {
		t.Fatalf("highest leaf index is %d with %d rows; the mirror has a gap", maxIndex, indexed)
	}

	distinct := zkQueryInt(t, s,
		`SELECT count(DISTINCT leaf_index) FROM zk_commitments WHERE chain_id = $1`, chainID)
	if distinct != ingestCommitments {
		t.Fatalf("%d distinct leaf indices for %d rows; the mirror duplicated a leaf", distinct, indexed)
	}

	perSecond := float64(indexed) / catchUp.Seconds()
	t.Logf("ingest baseline: %d events produced in %v, indexed in %v across %d steps (%.0f events/s)",
		indexed, produced.Round(time.Millisecond), catchUp.Round(time.Millisecond), steps, perSecond)

	// Not a threshold anyone should treat as a service level — testnet density is not mainnet
	// density, as §3 records. It is a floor that catches a collapse rather than a slow drift.
	if perSecond < 10 {
		t.Errorf("indexed %.1f events/s, which is slow enough to suggest something is wrong", perSecond)
	}
}
