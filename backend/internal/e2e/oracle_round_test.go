//go:build e2e

package e2e

import (
	"math/big"
	"testing"
)

func ether(n int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(n), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
}

// A full round against a real chain: three nodes registered and staked, three submissions signed
// in Go with real key material, settled on-chain.
//
// The signature is the point. Every unit test signs with Foundry's vm.sign against a
// Foundry-deployed address; this signs with the same code path a node binary will use, against a
// script-deployed contract, and lets the contract verify it. A disagreement between the Go and
// Solidity EIP-712 encodings — domain, typehash, field order, the v offset — surfaces only here.
func TestOracleRoundSettlesWithGoSignedSubmissions(t *testing.T) {
	requireDeps(t)
	d := deployOracle(t)
	nodes := registerNodes(t, d, 3)

	send(t, deployerKey, d.OracleRounds, "openRound(bytes32)", feedIDHex())
	roundID := castCallUint(t, d.OracleRounds, "currentRoundId(bytes32)(uint256)", feedIDHex()).Int64()

	submitSigned(t, d, nodes[0], roundID, ether(3100))
	submitSigned(t, d, nodes[1], roundID, ether(2900))
	submitSigned(t, d, nodes[2], roundID, ether(3000))

	send(t, deployerKey, d.OracleRounds, "settleRound(uint256)", fmt64(roundID))

	value, settledAt, settledRound := readFeed(t, d, "3600")
	if value.Cmp(ether(3000)) != 0 {
		t.Errorf("settled value = %s, want 3000e18 — the median, not the mean", value)
	}
	if settledRound.Int64() != roundID {
		t.Errorf("settled round = %s, want %d", settledRound, roundID)
	}
	if settledAt.Sign() == 0 {
		t.Error("settledAt was not recorded")
	}
}

// The eligibility snapshot has to survive a real chain, not just a Foundry harness: a node that
// leaves mid-round must not shrink the denominator it is measured against.
func TestOracleRoundDeactivationDoesNotShrinkQuorumOnChain(t *testing.T) {
	requireDeps(t)
	d := deployOracle(t)
	nodes := registerNodes(t, d, 6)

	send(t, deployerKey, d.OracleRounds, "openRound(bytes32)", feedIDHex())
	roundID := castCallUint(t, d.OracleRounds, "currentRoundId(bytes32)(uint256)", feedIDHex()).Int64()

	// Three of six submit: short of the 2/3 of six that quorum requires.
	submitSigned(t, d, nodes[0], roundID, ether(3000))
	submitSigned(t, d, nodes[1], roundID, ether(3010))
	submitSigned(t, d, nodes[2], roundID, ether(2990))

	// The other three leave, hoping three-of-three becomes a quorum.
	for _, node := range nodes[3:] {
		send(t, node.keyHex, d.OracleStaking, "requestUnstake(uint256)", minStake)
	}
	if active := castCallUint(t, d.OracleStaking, "activeNodeCount()(uint256)"); active.Int64() != 3 {
		t.Fatalf("active node count = %s, want 3 — the live set really did shrink", active)
	}

	mineBlocks(t, 1)
	cast(t, "rpc", "--rpc-url", anvilRPC, "evm_increaseTime", "400")
	mineBlocks(t, 1)

	send(t, deployerKey, d.OracleRounds, "settleRound(uint256)", fmt64(roundID))

	// A settled round would mean the denominator moved when the three nodes left.
	if last := castCallUint(t, d.OracleRounds, "lastSettledRoundId(bytes32)(uint256)", feedIDHex()); last.Sign() != 0 {
		t.Fatalf("round %d settled despite quorum being judged against six nodes", roundID)
	}
}

// The reader fails closed. A price that is present but stale must not be returned.
func TestOracleReaderRejectsStaleValueOnChain(t *testing.T) {
	requireDeps(t)
	d := deployOracle(t)
	nodes := registerNodes(t, d, 3)

	send(t, deployerKey, d.OracleRounds, "openRound(bytes32)", feedIDHex())
	roundID := castCallUint(t, d.OracleRounds, "currentRoundId(bytes32)(uint256)", feedIDHex()).Int64()

	for _, node := range nodes {
		submitSigned(t, d, node, roundID, ether(3000))
	}
	send(t, deployerKey, d.OracleRounds, "settleRound(uint256)", fmt64(roundID))

	readFeed(t, d, "3600") // fresh: fine

	cast(t, "rpc", "--rpc-url", anvilRPC, "evm_increaseTime", "7200")
	mineBlocks(t, 1)

	if out, err := castErr(t, "call", "--rpc-url", anvilRPC, d.OracleRounds,
		"getValue(bytes32,uint256)(uint256,uint256,uint256)", feedIDHex(), "3600"); err == nil {
		t.Fatalf("a two-hour-old value was returned under a one-hour bound: %s", out)
	}
}
