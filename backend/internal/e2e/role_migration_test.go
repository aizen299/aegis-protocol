//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// The role-migration procedure from docs/v0.3-governance-plan.md §2.4, run against a real chain.
//
// contracts/test/integration/RoleMigration.t.sol proves the mechanics in isolation. This proves the
// runbook against separately deployed contracts, which is the form an operator actually performs:
// two deployments, addresses copied between them, and a proposal that reaches across.
func TestRoleMigrationOnALocalDeployment(t *testing.T) {
	requireDeps(t)

	vault := deployFresh(t)
	gov := deployGovernance(t)

	const vaultManagerRole = "VAULT_MANAGER_ROLE"
	role := cast(t, "keccak", vaultManagerRole)

	// Stage 1: the timelock receives the role while the deployer — standing in for the multisig —
	// keeps it. Nothing has been given up yet.
	send(t, deployerKey, vault.VaultProxy, "grantRole(bytes32,address)", role, gov.Timelock)

	if !hasRole(t, vault.VaultProxy, role, gov.Timelock) {
		t.Fatal("the timelock did not receive the role")
	}
	if !hasRole(t, vault.VaultProxy, role, deployerAddr) {
		t.Fatal("the deployer lost its own role")
	}

	// The role goes to the timelock, never the governor: the timelock makes the call.
	if hasRole(t, vault.VaultProxy, role, gov.Governor) {
		t.Fatal("the governor was granted a protocol role")
	}

	// Stage 2: a proposal exercises the role end to end.
	send(t, deployerKey, gov.AegisToken, "delegate(address)", deployerAddr)

	const newCap = "777000000000000000000"
	payload := cast(t, "calldata", "setDepositCap(uint256)", newCap)

	send(t, deployerKey, gov.Governor,
		"propose((uint256,bytes32,uint256,bytes),string,string)",
		action(chainID, vault.VaultProxy, "0", payload),
		"Migrate the vault manager role", "section 2.4 stage 2")

	proposalID := "1"
	advanceTime(t, votingDelaySeconds+1)
	send(t, deployerKey, gov.Governor, "castVote(uint256,uint8,string)", proposalID, "1", "aye")
	advanceTime(t, votingPeriodSeconds+1)
	send(t, deployerKey, gov.Governor, "queue(uint256)", proposalID)

	// Still observable, still not in effect. That is what the delay buys.
	if got := depositCap(t, vault.VaultProxy); got == newCap {
		t.Fatal("the cap moved before the delay elapsed")
	}

	advanceTime(t, timelockSeconds+1)
	send(t, deployerKey, gov.Governor, "execute(uint256)", proposalID)

	if got := depositCap(t, vault.VaultProxy); got != newCap {
		t.Fatalf("deposit cap = %s, want %s — governance could not exercise the role", got, newCap)
	}

	// Stage 3: only now, with governance seen to work, does the deployer step back.
	send(t, deployerKey, vault.VaultProxy, "renounceRole(bytes32,address)", role, deployerAddr)

	if hasRole(t, vault.VaultProxy, role, deployerAddr) {
		t.Fatal("the deployer still holds the role after renouncing")
	}
	sendExpectingFailure(t, deployerKey, vault.VaultProxy, "setDepositCap(uint256)", "1")

	// And governance still holds what it was migrated.
	if !hasRole(t, vault.VaultProxy, role, gov.Timelock) {
		t.Fatal("the timelock lost the role")
	}

	// What never moves: the slasher key is rotatable in minutes by design, and a multi-day timelock
	// is strictly worse for the threat it answers.
	slasher := cast(t, "keccak", "SLASHER_ROLE")
	if hasRole(t, vault.VaultProxy, slasher, gov.Timelock) {
		t.Fatal("the slasher role was migrated to a multi-day timelock")
	}
}

// cast renders a uint256 as "<decimal> [<scientific>]"; only the first field is the value.
func depositCap(t *testing.T, vaultAddress string) string {
	t.Helper()
	return strings.Fields(call(t, vaultAddress, "depositCap()(uint256)"))[0]
}

func hasRole(t *testing.T, contract, role, account string) bool {
	t.Helper()
	return strings.HasPrefix(call(t, contract, "hasRole(bytes32,address)(bool)", role, account), "true")
}
