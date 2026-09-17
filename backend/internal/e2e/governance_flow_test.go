//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// The full governance path against a real chain: propose, vote on snapshotted weight, queue into
// the timelock, wait out the delay, execute. Unit tests prove each step against a fixture; this is
// the only place the deployed proxies, the real clock, and the real role wiring meet.
func TestGovernanceProposalReachesExecution(t *testing.T) {
	requireDeps(t)
	d := deployGovernance(t)

	delegateVotes(t, deployerKey, d.AegisToken, deployerAddr)

	payload := cast(t, "calldata", "setValue(uint256)", "42")
	proposalAction := action(chainID, d.GovernedTarget, "0", payload)

	send(t, deployerKey, d.Governor,
		"propose((uint256,bytes32,uint256,bytes),string,string)",
		proposalAction, "Set the value", "end to end")

	proposalID := "1"
	if got := proposalState(t, d, proposalID); got != statePending {
		t.Fatalf("state after propose = %d, want pending", got)
	}

	advanceTime(t, votingDelaySeconds+1)
	if got := proposalState(t, d, proposalID); got != stateActive {
		t.Fatalf("state after the voting delay = %d, want active", got)
	}

	send(t, deployerKey, d.Governor, "castVote(uint256,uint8,string)", proposalID, "1", "for")

	advanceTime(t, votingPeriodSeconds+1)
	if got := proposalState(t, d, proposalID); got != stateSucceeded {
		t.Fatalf("state after the voting period = %d, want succeeded", got)
	}

	send(t, deployerKey, d.Governor, "queue(uint256)", proposalID)
	if got := proposalState(t, d, proposalID); got != stateQueued {
		t.Fatalf("state after queue = %d, want queued", got)
	}

	operationID := proposalOperationID(t, d, proposalID)
	if got := operationState(t, d, operationID); got != opScheduled {
		t.Fatalf("operation state after queue = %d, want scheduled", got)
	}

	// The delay is the point. If this succeeded, the timelock would be decoration.
	sendExpectingFailure(t, deployerKey, d.Governor, "execute(uint256)", proposalID)
	if got := call(t, d.GovernedTarget, "value()(uint256)"); toInt(t, got) != 0 {
		t.Fatalf("the action ran before its delay elapsed")
	}

	advanceTime(t, timelockSeconds+1)
	send(t, deployerKey, d.Governor, "execute(uint256)", proposalID)

	if got := proposalState(t, d, proposalID); got != stateExecuted {
		t.Fatalf("state after execute = %d, want executed", got)
	}
	if got := operationState(t, d, operationID); got != opExecuted {
		t.Fatalf("operation state after execute = %d, want executed", got)
	}
	if got := toInt(t, call(t, d.GovernedTarget, "value()(uint256)")); got != 42 {
		t.Fatalf("target value = %d, want 42", got)
	}

	// The timelock performs the call, so it is the account a governed contract sees. This is what
	// makes protocol roles belong on the timelock rather than the governor.
	caller := strings.ToLower(call(t, d.GovernedTarget, "lastCaller()(address)"))
	if caller != d.Timelock {
		t.Fatalf("executed call came from %s, want the timelock %s", caller, d.Timelock)
	}
}

// A remote proposal never performs a local call: with no route it cannot even be proposed, and with
// one it is dispatched, not executed.
func TestGovernanceRemoteProposalNeverPerformsALocalCall(t *testing.T) {
	requireDeps(t)
	d := deployGovernance(t)

	delegateVotes(t, deployerKey, d.AegisToken, deployerAddr)
	payload := cast(t, "calldata", "setValue(uint256)", "99")

	sendExpectingFailure(t, deployerKey, d.Governor,
		"propose((uint256,bytes32,uint256,bytes),string,string)",
		action(chainID+2, d.GovernedTarget, "0", payload), "Unrouted", "no route")

	send(t, deployerKey, d.Governor,
		"propose((uint256,bytes32,uint256,bytes),string,string)",
		action(chainID+1, d.GovernedTarget, "0", payload), "Remote", "another chain")

	proposalID := "1"
	advanceTime(t, votingDelaySeconds+1)
	send(t, deployerKey, d.Governor, "castVote(uint256,uint8,string)", proposalID, "1", "")
	advanceTime(t, votingPeriodSeconds+1)
	send(t, deployerKey, d.Governor, "queue(uint256)", proposalID)
	advanceTime(t, timelockSeconds+1)
	send(t, deployerKey, d.Governor, "execute(uint256)", proposalID)

	if got := toInt(t, call(t, d.GovernedTarget, "value()(uint256)")); got != 0 {
		t.Fatalf("a remote action landed locally: target value = %d", got)
	}
	if got := proposalState(t, d, proposalID); got != stateDispatched {
		t.Fatalf("state = %d, want dispatched", got)
	}
	if got := operationState(t, d, proposalOperationID(t, d, proposalID)); got != opDispatched {
		t.Fatalf("operation state = %d, want dispatched", got)
	}
}

func TestGovernanceCancelledProposalClearsTheQueue(t *testing.T) {
	requireDeps(t)
	d := deployGovernance(t)

	delegateVotes(t, deployerKey, d.AegisToken, deployerAddr)

	payload := cast(t, "calldata", "setValue(uint256)", "7")
	proposalAction := action(chainID, d.GovernedTarget, "0", payload)

	send(t, deployerKey, d.Governor,
		"propose((uint256,bytes32,uint256,bytes),string,string)",
		proposalAction, "Doomed", "cancelled while queued")

	proposalID := "1"
	advanceTime(t, votingDelaySeconds+1)
	send(t, deployerKey, d.Governor, "castVote(uint256,uint8,string)", proposalID, "1", "")
	advanceTime(t, votingPeriodSeconds+1)

	send(t, deployerKey, d.Governor, "queue(uint256)", proposalID)
	operationID := proposalOperationID(t, d, proposalID)

	send(t, deployerKey, d.Governor, "cancel(uint256)", proposalID)

	if got := proposalState(t, d, proposalID); got != stateCancelled {
		t.Fatalf("state after cancel = %d, want cancelled", got)
	}
	if got := operationState(t, d, operationID); got != opCancelled {
		t.Fatalf("operation state after cancel = %d, want cancelled", got)
	}

	advanceTime(t, timelockSeconds+1)
	sendExpectingFailure(t, deployerKey, d.Governor, "execute(uint256)", proposalID)

	if got := toInt(t, call(t, d.GovernedTarget, "value()(uint256)")); got != 0 {
		t.Fatalf("a cancelled proposal executed: target value = %d", got)
	}
}
