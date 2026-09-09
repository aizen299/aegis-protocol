//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	votingDelaySeconds  = 24 * 60 * 60
	votingPeriodSeconds = 7 * 24 * 60 * 60
	timelockSeconds     = 2 * 24 * 60 * 60
)

// Proposal states, mirroring IGovernor.ProposalState.
const (
	stateNone = iota
	statePending
	stateActive
	stateSucceeded
	stateDefeated
	stateQueued
	stateDispatched
	stateExecuted
	stateFailed
	stateCancelled
)

// Timelock operation states, mirroring ITimelock.OperationState.
const (
	opNone = iota
	opScheduled
	opExecuted
	opCancelled
)

type governanceDeployment struct {
	AegisToken      string `json:"aegisToken"`
	ChainID         int64  `json:"chainId"`
	DeployedAtBlock uint64 `json:"deployedAtBlock"`
	GovernedTarget  string `json:"governedTarget"`
	Governor        string `json:"governor"`
	Timelock        string `json:"timelock"`
}

func deployGovernance(t *testing.T) governanceDeployment {
	t.Helper()
	root := repoRoot(t)

	cmd := exec.Command("forge", "script",
		"script/DeployGovernanceLocal.s.sol:DeployGovernanceLocal",
		"--rpc-url", anvilRPC, "--broadcast", "--silent")
	cmd.Dir = filepath.Join(root, "contracts")
	cmd.Env = append(os.Environ(), "PRIVATE_KEY="+deployerKey)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("governance deploy failed: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(filepath.Join(root, "contracts", "deployments", "governance-31337.json"))
	if err != nil {
		t.Fatalf("read governance deployment: %v", err)
	}

	var d governanceDeployment
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("parse governance deployment: %v", err)
	}
	if d.Governor == "" || d.Timelock == "" || d.AegisToken == "" || d.GovernedTarget == "" {
		t.Fatalf("governance deployment artifact is incomplete: %s", raw)
	}

	d.AegisToken = canonical(t, d.AegisToken)
	d.Governor = canonical(t, d.Governor)
	d.Timelock = canonical(t, d.Timelock)
	d.GovernedTarget = canonical(t, d.GovernedTarget)
	return d
}

// advanceTime moves the chain clock forward and mines, which is what makes the timestamp visible.
// Anvil applies an increase only on the next mined block.
func advanceTime(t *testing.T, seconds int64) {
	t.Helper()
	cast(t, "rpc", "--rpc-url", anvilRPC, "evm_increaseTime", fmt.Sprintf("0x%x", seconds))
	mineBlocks(t, 1)
}

// action encodes an IGovernor.Action tuple for cast.
func action(targetChainID int64, target string, value string, payload string) string {
	return fmt.Sprintf("(%d,%s,%s,%s)", targetChainID, addressAsBytes32(target), value, payload)
}

// addressAsBytes32 left-pads an address into the 32-byte identity the protocol uses across layers.
func addressAsBytes32(address string) string {
	return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(address, "0x")
}

func proposalState(t *testing.T, d governanceDeployment, proposalID string) int64 {
	t.Helper()
	return toInt(t, call(t, d.Governor, "state(uint256)(uint8)", proposalID))
}

func operationState(t *testing.T, d governanceDeployment, operationID string) int64 {
	t.Helper()
	raw := call(t, d.Timelock, "operationOf(uint256)((uint256,bytes32,uint256,uint48,uint8,bytes))",
		operationID)
	fields := tupleFields(raw)
	if len(fields) < 5 {
		t.Fatalf("unexpected operationOf output: %q", raw)
	}
	return toInt(t, fields[4])
}

// proposalOperationID reads the timelock operation a proposal was queued into.
func proposalOperationID(t *testing.T, d governanceDeployment, proposalID string) string {
	t.Helper()
	raw := call(t, d.Governor,
		"proposalOf(uint256)((address,uint48,uint48,uint48,uint8,uint256,uint256,uint256,uint256,(uint256,bytes32,uint256,bytes)))",
		proposalID)
	fields := tupleFields(raw)
	if len(fields) < 6 {
		t.Fatalf("unexpected proposalOf output: %q", raw)
	}
	return fields[5]
}

// tupleFields splits cast's top-level tuple rendering. cast prints one field per line for a struct
// return, which is stabler to parse than the single-line form.
func tupleFields(raw string) []string {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) > 1 {
		out := make([]string, 0, len(lines))
		for _, line := range lines {
			out = append(out, strings.TrimSpace(line))
		}
		return out
	}

	trimmed := strings.Trim(strings.TrimSpace(raw), "()")
	parts := strings.Split(trimmed, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func toInt(t *testing.T, raw string) int64 {
	t.Helper()

	value, ok := new(big.Int).SetString(strings.Fields(strings.TrimSpace(raw))[0], 0)
	if !ok {
		t.Fatalf("parse integer from %q", raw)
	}
	return value.Int64()
}

// sendExpectingFailure asserts a transaction is rejected. The chain refusing is the assertion.
func sendExpectingFailure(t *testing.T, key, to, sig string, args ...string) {
	t.Helper()

	full := append([]string{"send", "--rpc-url", anvilRPC, "--private-key", key, to, sig}, args...)
	if out, err := exec.Command("cast", full...).CombinedOutput(); err == nil {
		t.Fatalf("expected %s to be rejected, but it succeeded:\n%s", sig, out)
	}
}
