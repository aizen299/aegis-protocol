//go:build e2e

package e2e

import (
	"context"
	"io"
	"testing"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/chain/evm"
	oraclesvc "github.com/aizen299/aegis-protocol/backend/internal/oracle"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// The whole loop in one pass: three nodes submit, one of them reports a price well outside the
// others, the round settles, the indexer records it, the aggregator verifies every signature and
// medianizes independently, decides a penalty, and the executor puts it on chain.
//
// Every component here has been tested against a fake of its neighbour. This is the only place they
// meet, and in v0.1 that seam is where every real defect turned up.
func TestOutlierIsSlashedEndToEnd(t *testing.T) {
	requireDeps(t)
	d := deployOracle(t)
	nodes := registerNodes(t, d, 3)
	s := setupOracleStack(t, d)

	send(t, deployerKey, d.OracleRounds, "openRound(bytes32)", feedIDHex())
	roundID := castCallUint(t, d.OracleRounds, "currentRoundId(bytes32)(uint256)", feedIDHex()).Int64()

	// Two honest readings and one 20% above them: well beyond the 5% tolerance.
	submitSigned(t, d, nodes[0], roundID, ether(3000))
	submitSigned(t, d, nodes[1], roundID, ether(3000))
	submitSigned(t, d, nodes[2], roundID, ether(3600))

	send(t, deployerKey, d.OracleRounds, "settleRound(uint256)", fmt64(roundID))
	s.indexToHead(t)

	// The contract's own median is unmoved by the outlier — that is what medianization buys.
	assertRound(t, s, roundID, "settled", ether(3000), 3, 3)

	aggregator, executor := s.aggregation(t, d)

	analysed, err := aggregator.Step(context.Background())
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if analysed != 1 {
		t.Fatalf("analysed %d rounds, want 1", analysed)
	}

	// The decision exists in Postgres before anything reaches the chain.
	decisions := queryInt(t, s,
		`SELECT count(*) FROM oracle_slashings WHERE chain_id = $1 AND executed_at IS NULL`, chainID)
	if decisions != 1 {
		t.Fatalf("recorded %d decisions, want exactly the outlier", decisions)
	}
	decidedNode := queryString(t, s,
		`SELECT node_address FROM oracle_slashings WHERE chain_id = $1`, chainID)
	if decidedNode != nodes[2].address {
		t.Fatalf("decided against %s, want the outlier %s", decidedNode, nodes[2].address)
	}
	reason := queryString(t, s, `SELECT reason FROM oracle_slashings WHERE chain_id = $1`, chainID)
	if reason != oraclesvc.ReasonOutlier {
		t.Errorf("reason = %q, want %q", reason, oraclesvc.ReasonOutlier)
	}

	// The service's own median must agree with the contract's, or the signature verification or
	// the medianization disagree with the chain.
	serviceValue := queryString(t, s,
		`SELECT service_value::text FROM oracle_rounds WHERE chain_id = $1 AND round_id = $2`,
		chainID, roundID)
	if serviceValue != ether(3000).String() {
		t.Errorf("service value = %s, want the same median the contract settled", serviceValue)
	}
	if mismatch := queryInt(t, s,
		`SELECT count(*) FROM oracle_rounds WHERE chain_id = $1 AND value_mismatch`, chainID); mismatch != 0 {
		t.Error("the service disagreed with the settled value")
	}

	// Every signature the contract accepted must verify off-chain too.
	verified := queryInt(t, s,
		`SELECT count(*) FROM oracle_submissions WHERE chain_id = $1 AND signature_valid`, chainID)
	if verified != 3 {
		t.Fatalf("%d of 3 signatures verified off-chain; the Go and Solidity digests disagree", verified)
	}

	stakeBefore := queryString(t, s,
		`SELECT staked_amount::text FROM oracle_nodes WHERE chain_id = $1 AND address = $2`,
		chainID, nodes[2].address)

	submitted, err := executor.Step(context.Background())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if submitted != 1 {
		t.Fatalf("submitted %d slashes, want 1", submitted)
	}

	mineBlocks(t, 2)
	s.indexToHead(t)

	// 1% of a 10,000-token stake.
	stakeAfter := queryString(t, s,
		`SELECT staked_amount::text FROM oracle_nodes WHERE chain_id = $1 AND address = $2`,
		chainID, nodes[2].address)
	if stakeAfter == stakeBefore {
		t.Fatalf("the outlier's stake is unchanged at %s", stakeAfter)
	}
	if stakeAfter != "9900000000000000000000" {
		t.Errorf("stake = %s, want 9900e18 after a 1%% penalty", stakeAfter)
	}

	// The honest nodes are untouched.
	for _, honest := range nodes[:2] {
		stake := queryString(t, s,
			`SELECT staked_amount::text FROM oracle_nodes WHERE chain_id = $1 AND address = $2`,
			chainID, honest.address)
		if stake != minStake {
			t.Errorf("honest node %s was slashed: stake = %s", honest.address, stake)
		}
	}

	// The decision and its execution are the same audit row, linked to the round that caused it.
	executed := queryInt(t, s,
		`SELECT count(*) FROM oracle_slashings
		 WHERE chain_id = $1 AND executed_at IS NOT NULL AND round_id = $2 AND tx_hash IS NOT NULL`,
		chainID, roundID)
	if executed != 1 {
		t.Fatalf("the decision was not reconciled with its execution")
	}
}

// The contract permits one penalty per node per round, which is what makes the executor's retry
// safe. Running the executor again over the same decision must not take the stake twice.
func TestExecutorRetryDoesNotDoubleSlash(t *testing.T) {
	requireDeps(t)
	d := deployOracle(t)
	nodes := registerNodes(t, d, 3)
	s := setupOracleStack(t, d)

	send(t, deployerKey, d.OracleRounds, "openRound(bytes32)", feedIDHex())
	roundID := castCallUint(t, d.OracleRounds, "currentRoundId(bytes32)(uint256)", feedIDHex()).Int64()
	submitSigned(t, d, nodes[0], roundID, ether(3000))
	submitSigned(t, d, nodes[1], roundID, ether(3000))
	submitSigned(t, d, nodes[2], roundID, ether(3600))
	send(t, deployerKey, d.OracleRounds, "settleRound(uint256)", fmt64(roundID))
	s.indexToHead(t)

	aggregator, executor := s.aggregation(t, d)
	if _, err := aggregator.Step(context.Background()); err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if _, err := executor.Step(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	mineBlocks(t, 2)

	// Reopen the decision, exactly as a crash between submitting and recording would leave it.
	var reopened int
	if err := s.store.QueryRowForTest(context.Background(),
		`UPDATE oracle_slashings SET submitted_at = NULL, tx_hash = NULL
		 WHERE chain_id = $1 RETURNING 1`, chainID).Scan(&reopened); err != nil {
		t.Fatalf("reopen decision: %v", err)
	}

	if _, err := executor.Step(context.Background()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	mineBlocks(t, 2)
	s.indexToHead(t)

	stake := queryString(t, s,
		`SELECT staked_amount::text FROM oracle_nodes WHERE chain_id = $1 AND address = $2`,
		chainID, nodes[2].address)
	if stake != "9900000000000000000000" {
		t.Fatalf("stake = %s, want a single 1%% penalty despite the retry", stake)
	}

	abandoned := queryInt(t, s,
		`SELECT count(*) FROM oracle_slashings WHERE chain_id = $1 AND abandoned_at IS NOT NULL`, chainID)
	if abandoned != 1 {
		t.Errorf("the retry did not close the decision the chain had already satisfied")
	}
}

// aggregation builds the real aggregator and executor over the same store the indexer wrote to,
// with the deployer's key — which DeployOracleLocal granted SLASHER_ROLE.
func (s *oracleStack) aggregation(t *testing.T, d oracleDeployment) (*oraclesvc.Aggregator, *oraclesvc.Executor) {
	t.Helper()

	roundsID, err := types.IdentityFromEVMHex(d.OracleRounds)
	if err != nil {
		t.Fatalf("rounds address: %v", err)
	}
	stakingID, err := types.IdentityFromEVMHex(d.OracleStaking)
	if err != nil {
		t.Fatalf("staking address: %v", err)
	}

	key, err := evm.NodeKeyFromHex(deployerKey)
	if err != nil {
		t.Fatalf("slasher key: %v", err)
	}
	slasher, err := evm.NewSlasher(s.client, stakingID, key, 0)
	if err != nil {
		t.Fatalf("slasher: %v", err)
	}

	log := zerolog.New(io.Discard)
	aggregator := oraclesvc.NewAggregator(s.store, evm.NewSubmissionVerifier(roundsID), log,
		oraclesvc.AggregatorOptions{ChainID: chainID, RoundsContract: roundsID})
	executor := oraclesvc.NewExecutor(s.store, slasher, log,
		oraclesvc.ExecutorOptions{ChainID: chainID, MaxAttempts: 3})

	return aggregator, executor
}
