//go:build e2e

package e2e

import (
	"context"
	"math/big"
	"testing"

	oraclesvc "github.com/aizen299/aegis-protocol/backend/internal/oracle"
)

const roundDurationSeconds = 300

// runRound opens a round, has `submitters` submit, and settles it. When `pastDeadline` is set the
// chain is advanced beyond the deadline first, so the round ran its full window; otherwise it is
// settled the moment quorum allows.
func runRound(t *testing.T, s *oracleStack, d oracleDeployment, submitters []oracleNode, pastDeadline bool) int64 {
	t.Helper()
	send(t, deployerKey, d.OracleRounds, "openRound(bytes32)", feedIDHex())
	roundID := castCallUint(t, d.OracleRounds, "currentRoundId(bytes32)(uint256)", feedIDHex()).Int64()

	for _, node := range submitters {
		submitSigned(t, d, node, roundID, ether(3000))
	}
	if pastDeadline {
		advanceTime(t, roundDurationSeconds+1)
	}
	send(t, deployerKey, d.OracleRounds, "settleRound(uint256)", fmt64(roundID))
	s.indexToHead(t)
	return roundID
}

// judgeAndExecute runs the missed-round pass and the executor, then indexes what landed.
func judgeAndExecute(t *testing.T, s *oracleStack, agg *oraclesvc.Aggregator, exec *oraclesvc.Executor) {
	t.Helper()
	if _, err := agg.JudgeMisses(context.Background()); err != nil {
		t.Fatalf("judge misses: %v", err)
	}
	if _, err := exec.Step(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	mineBlocks(t, 2)
	s.indexToHead(t)
}

func stakeOf(t *testing.T, s *oracleStack, node oracleNode) *big.Int {
	t.Helper()
	v, ok := new(big.Int).SetString(queryString(t, s,
		`SELECT staked_amount::text FROM oracle_nodes WHERE chain_id = $1 AND address = $2`,
		chainID, node.address), 10)
	if !ok {
		t.Fatalf("stake of %s is not a number", node.address)
	}
	return v
}

func minus(stake *big.Int, bps int64) *big.Int {
	cut := new(big.Int).Mul(stake, big.NewInt(bps))
	cut.Div(cut, big.NewInt(10_000))
	return new(big.Int).Sub(stake, cut)
}

// ORC-1 end to end: a node that stays silent through three rounds that ran to their deadlines pays
// 0.5%, 0.5%, and then 10% — the third replacing its 0.5% rather than adding to it — while the nodes
// that submitted pay nothing.
func TestMissedRoundsAreSlashedEndToEnd(t *testing.T) {
	requireDeps(t)
	d := deployOracle(t)
	nodes := registerNodes(t, d, 4)
	s := setupOracleStack(t, d)
	agg, exec := s.aggregation(t, d)

	honest, silent := nodes[:3], nodes[3]
	s.indexToHead(t)
	expected := stakeOf(t, s, silent)

	for i, bps := range []int64{50, 50, 1000} {
		roundID := runRound(t, s, d, honest, true)
		judgeAndExecute(t, s, agg, exec)

		expected = minus(expected, bps)
		if got := stakeOf(t, s, silent); got.Cmp(expected) != 0 {
			t.Fatalf("round %d (id %d): silent stake = %s, want %s after %d bps", i+1, roundID, got, expected, bps)
		}
	}

	reasons := queryString(t, s,
		`SELECT string_agg(reason, ',' ORDER BY round_id) FROM oracle_slashings
		 WHERE chain_id = $1 AND node_address = $2 AND executed_at IS NOT NULL`, chainID, silent.address)
	want := oraclesvc.ReasonMissedRound + "," + oraclesvc.ReasonMissedRound + "," + oraclesvc.ReasonConsecutiveMisses
	if reasons != want {
		t.Errorf("executed reasons = %q, want %q", reasons, want)
	}

	for _, node := range honest {
		if got := stakeOf(t, s, node); got.String() != minStake {
			t.Errorf("honest node %s was slashed: stake %s", node.address, got)
		}
	}

	// The count the Nodes page shows is now real, not a column nothing wrote.
	if missed := queryInt(t, s,
		`SELECT count(*) FROM oracle_round_outcomes WHERE chain_id = $1 AND node = $2 AND outcome = 'missed'`,
		chainID, silent.address); missed != 3 {
		t.Errorf("recorded misses = %d, want 3", missed)
	}
}

// The three excuses, each on a real chain: a round settled the moment quorum landed, a node removed by
// the admin before the deadline, and a round nobody submitted to. None may cost anyone anything.
func TestExcusedSilencesAreNotSlashedEndToEnd(t *testing.T) {
	requireDeps(t)
	d := deployOracle(t)
	nodes := registerNodes(t, d, 4)
	s := setupOracleStack(t, d)
	agg, exec := s.aggregation(t, d)

	// 1. Settled before its deadline: nodes[3] was cut off, not silent by choice.
	early := runRound(t, s, d, nodes[:3], false)
	judgeAndExecute(t, s, agg, exec)

	// 2. Removed by the admin before the deadline: nodes[3] could not submit.
	send(t, deployerKey, d.OracleRounds, "openRound(bytes32)", feedIDHex())
	removedRound := castCallUint(t, d.OracleRounds, "currentRoundId(bytes32)(uint256)", feedIDHex()).Int64()
	for _, node := range nodes[:3] {
		submitSigned(t, d, node, removedRound, ether(3000))
	}
	send(t, deployerKey, d.OracleStaking, "deactivate(address,bytes32)", nodes[3].address, reasonHex("MANUAL"))
	advanceTime(t, roundDurationSeconds+1)
	send(t, deployerKey, d.OracleRounds, "settleRound(uint256)", fmt64(removedRound))
	s.indexToHead(t)
	judgeAndExecute(t, s, agg, exec)

	// 3. Nobody submitted: an outage. The round fails, and it ran to its deadline.
	outage := runRound(t, s, d, nil, true)
	judgeAndExecute(t, s, agg, exec)

	if state := queryString(t, s,
		`SELECT state FROM oracle_rounds WHERE chain_id = $1 AND round_id = $2`, chainID, outage); state != "failed" {
		t.Fatalf("outage round state = %q, want failed", state)
	}

	if slashes := queryInt(t, s, `SELECT count(*) FROM oracle_slashings WHERE chain_id = $1`, chainID); slashes != 0 {
		t.Fatalf("%d slashes recorded, want none", slashes)
	}
	for _, node := range nodes {
		if got := stakeOf(t, s, node); got.String() != minStake {
			t.Errorf("node %s lost stake: %s", node.address, got)
		}
	}

	for _, tc := range []struct {
		round int64
		node  oracleNode
		why   string
	}{
		{early, nodes[3], "the round settled before its deadline"},
		{removedRound, nodes[3], "removed before the deadline: MANUAL"},
		{outage, nodes[0], "no node submitted"},
	} {
		got := queryString(t, s,
			`SELECT outcome || ':' || why FROM oracle_round_outcomes
			 WHERE chain_id = $1 AND round_id = $2 AND node = $3`, chainID, tc.round, tc.node.address)
		if got != "excused:"+tc.why {
			t.Errorf("round %d, node %s: outcome %q, want excused:%s", tc.round, tc.node.address, got, tc.why)
		}
	}
}
