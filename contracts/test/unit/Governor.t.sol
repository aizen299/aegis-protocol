// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IAccessControl} from "@openzeppelin/contracts/access/IAccessControl.sol";

import {IGovernor} from "../../src/governance/interfaces/IGovernor.sol";
import {ITimelock} from "../../src/governance/interfaces/ITimelock.sol";
import {GovernedTarget, GovernorFixture} from "../utils/GovernorFixture.sol";

contract GovernorTest is GovernorFixture {
    // --- the §7 constraint ---

    /// A non-local destination has nowhere to go in Phase 1, but the branch exists so that "local"
    /// is not baked into the type. See docs/project-spec.md §7.
    function test_crossChainProposalCannotExecuteLocally() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        IGovernor.Action memory action = _localAction(42);
        action.targetChainId = block.chainid + 1;

        vm.prank(alice);
        uint256 proposalId = governor.propose(action, "Remote", "");

        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        governor.queue(proposalId);
        skip(TIMELOCK_DELAY + 1);

        vm.expectRevert(
            abi.encodeWithSelector(ITimelock.CrossChainDispatchUnavailable.selector, block.chainid + 1)
        );
        governor.execute(proposalId);

        assertEq(target.value(), 0, "a remote proposal performed a local call");
    }

    /// A 32-byte target that does not fit an address must be rejected rather than truncated. The
    /// same check the backend's Identity performs.
    function test_targetWithHighBytesSetIsRejected() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        IGovernor.Action memory action = _localAction(42);
        action.target = bytes32(uint256(1) << 200 | uint256(uint160(address(target))));

        vm.prank(alice);
        uint256 proposalId = governor.propose(action, "Wide target", "");

        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        governor.queue(proposalId);
        skip(TIMELOCK_DELAY + 1);

        vm.expectRevert(abi.encodeWithSelector(ITimelock.TargetNotLocalAddress.selector, action.target));
        governor.execute(proposalId);
    }

    // --- the full path ---

    function test_proposalPassesQueuesAndExecutes() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.PENDING));

        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.SUCCEEDED));

        governor.queue(proposalId);
        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.QUEUED));

        skip(TIMELOCK_DELAY + 1);
        governor.execute(proposalId);

        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.EXECUTED));
        assertEq(target.value(), 42, "the proposal's action did not run");
    }

    // --- the timelock ---

    /// The timelock's whole purpose is that a passed proposal is observable before it takes effect.
    function test_executionBeforeTheTimelockElapsesReverts() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        governor.queue(proposalId);

        vm.expectRevert();
        governor.execute(proposalId);
        assertEq(target.value(), 0);
    }

    function test_executionTwiceReverts() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        governor.queue(proposalId);
        skip(TIMELOCK_DELAY + 1);
        governor.execute(proposalId);

        vm.expectRevert();
        governor.execute(proposalId);
    }

    function test_executionSurfacesARevertingTarget() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        IGovernor.Action memory action = _localAction(0);
        action.payload = abi.encodeCall(GovernedTarget.refuse, ());

        vm.prank(alice);
        uint256 proposalId = governor.propose(action, "Refuse", "");
        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        governor.queue(proposalId);
        skip(TIMELOCK_DELAY + 1);

        uint256 operationId = governor.proposalOf(proposalId).operationId;
        vm.expectRevert(abi.encodeWithSelector(ITimelock.ExecutionReverted.selector, operationId));
        governor.execute(proposalId);
    }

    /// Regression: at exactly `voteStart` the proposal reported ACTIVE while every vote reverted,
    /// because `getPastVotes` refuses a timepoint that is not yet in the past. A one-second window
    /// that advertised voting and refused it. Found by the invariant suite, not by unit tests —
    /// the fixture had always skipped `VOTING_DELAY + 1`, stepping over the boundary.
    function test_theProposalIsPendingAtExactlyTheSnapshot() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        skip(VOTING_DELAY);

        assertEq(
            uint48(block.timestamp),
            governor.proposalOf(proposalId).voteStart,
            "the test did not land on the boundary"
        );
        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.PENDING));

        skip(1);
        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.ACTIVE));

        vm.prank(bob);
        governor.castVote(proposalId, uint8(IGovernor.Support.FOR), "");
        assertGt(governor.proposalOf(proposalId).forVotes, 0);
    }

    // --- parameters ---
    //
    // These are the surface a captured admin would reach for, and setQuorumNumerator carries the
    // only validation in the group. An unexercised guard is how v0.2 nearly shipped without its
    // per-round slash check.

    function test_parameterSettersRequireTheAdminRole() public {
        vm.startPrank(carol);

        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, carol, bytes32(0)
            )
        );
        governor.setVotingDelay(1 days);

        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, carol, bytes32(0)
            )
        );
        governor.setVotingPeriod(1 days);

        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, carol, bytes32(0)
            )
        );
        governor.setProposalThreshold(1);

        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, carol, bytes32(0)
            )
        );
        governor.setQuorumNumerator(10);

        vm.stopPrank();
    }

    function test_adminCanSetEachParameter() public {
        vm.startPrank(admin);
        governor.setVotingDelay(2 days);
        governor.setVotingPeriod(3 days);
        governor.setProposalThreshold(1 ether);
        governor.setQuorumNumerator(10);
        vm.stopPrank();

        assertEq(governor.votingDelay(), 2 days);
        assertEq(governor.votingPeriod(), 3 days);
        assertEq(governor.proposalThreshold(), 1 ether);
        assertEq(governor.quorumNumerator(), 10);
    }

    function test_aZeroVotingPeriodIsRejected() public {
        vm.prank(admin);
        vm.expectRevert(IGovernor.ZeroValue.selector);
        governor.setVotingPeriod(0);
    }

    /// A quorum of zero passes anything and a quorum above the denominator can never be met. Both
    /// are unreachable states for a governance system, so neither is settable.
    function test_anOutOfRangeQuorumIsRejected() public {
        vm.startPrank(admin);

        vm.expectRevert(abi.encodeWithSelector(IGovernor.InvalidQuorumNumerator.selector, 0));
        governor.setQuorumNumerator(0);

        vm.expectRevert(abi.encodeWithSelector(IGovernor.InvalidQuorumNumerator.selector, 101));
        governor.setQuorumNumerator(101);

        vm.stopPrank();
    }

    /// Parameters must not move a vote that is already running: a proposal carries the window it
    /// was created with, and its quorum is measured against the snapshot.
    function test_aParameterChangeDoesNotMoveARunningVote() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        uint48 voteEnd = governor.proposalOf(proposalId).voteEnd;

        vm.startPrank(admin);
        governor.setVotingPeriod(1);
        governor.setVotingDelay(1);
        vm.stopPrank();

        assertEq(governor.proposalOf(proposalId).voteEnd, voteEnd, "a running vote was shortened");

        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.SUCCEEDED));
    }

    function test_theGovernorReportsItsWiring() public view {
        assertEq(governor.token(), address(token));
        assertEq(governor.timelock(), address(timelock));
        assertEq(governor.proposalCount(), 0);
    }

    // --- the seam between the governor and the timelock ---

    /// A cancelled proposal that left a live operation behind would still be executable by
    /// anything holding the executor role. "Cancelled can never reach executed" spans both
    /// contracts, so cancelling has to clear the queue as well as the proposal.
    function test_cancellingAQueuedProposalClearsTheTimelockQueue() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        governor.queue(proposalId);

        uint256 operationId = governor.proposalOf(proposalId).operationId;

        vm.prank(guardian);
        governor.cancel(proposalId);

        assertEq(
            uint8(timelock.operationOf(operationId).state),
            uint8(ITimelock.OperationState.CANCELLED),
            "the queue outlived the proposal"
        );

        skip(TIMELOCK_DELAY + 1);
        vm.prank(address(governor));
        vm.expectRevert(
            abi.encodeWithSelector(
                ITimelock.OperationNotScheduled.selector, operationId, ITimelock.OperationState.CANCELLED
            )
        );
        timelock.execute(operationId);
    }

    /// The governor is the timelock's only client. If anything else could execute a scheduled
    /// operation, the action would run while the proposal sat in the indexer still marked queued.
    function test_nobodyButTheGovernorCanDriveTheTimelock() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        governor.queue(proposalId);

        uint256 operationId = governor.proposalOf(proposalId).operationId;
        skip(TIMELOCK_DELAY + 1);

        vm.prank(carol);
        vm.expectRevert();
        timelock.execute(operationId);

        assertEq(target.value(), 0, "the timelock ran an action nobody was authorised to trigger");
        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.QUEUED));
    }

    /// Value belongs to the timelock, not the governor: replacing a governor must not mean moving
    /// a treasury.
    function test_valueBearingProposalSpendsFromTheTimelock() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);
        vm.deal(address(timelock), 5 ether);

        IGovernor.Action memory action = _localAction(0);
        action.value = 1 ether;
        action.payload = "";

        vm.prank(alice);
        uint256 proposalId = governor.propose(action, "Pay", "");
        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        governor.queue(proposalId);
        skip(TIMELOCK_DELAY + 1);
        governor.execute(proposalId);

        assertEq(target.received(), 1 ether);
        assertEq(address(timelock).balance, 4 ether);
        assertEq(address(governor).balance, 0, "the governor held value");
    }

    /// The delay a proposal was queued under is the delay it waits out, whoever changes the
    /// parameter afterwards.
    function test_aQueuedProposalIsUnaffectedByADelayChange() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        governor.queue(proposalId);

        uint48 executableAt = governor.proposalOf(proposalId).executableAt;

        vm.prank(admin);
        timelock.setDelay(1);

        skip(2);
        vm.expectRevert(
            abi.encodeWithSelector(ITimelock.DelayNotElapsed.selector, executableAt, block.timestamp)
        );
        governor.execute(proposalId);
    }

    // --- voting weight ---

    /// The property the whole design rests on: weight comes from a past snapshot, so tokens
    /// acquired after voting opens cannot vote. A flash loan cannot appear in a past checkpoint.
    function test_powerAcquiredAfterTheSnapshotCannotVote() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);

        skip(VOTING_DELAY + 1);
        // Carol buys in only now — after the snapshot at voteStart.
        _fund(carol, SUPPLY / 2);

        vm.prank(carol);
        vm.expectRevert(abi.encodeWithSelector(IGovernor.NoVotingPower.selector, carol));
        governor.castVote(proposalId, uint8(IGovernor.Support.AGAINST), "");
    }

    function test_powerSoldAfterTheSnapshotStillVotes() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        skip(VOTING_DELAY + 1);

        vm.prank(bob);
        token.transfer(carol, SUPPLY / 10);

        vm.prank(bob);
        governor.castVote(proposalId, uint8(IGovernor.Support.FOR), "");

        assertEq(governor.proposalOf(proposalId).forVotes, SUPPLY / 10);
    }

    function test_doubleVotingReverts() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        skip(VOTING_DELAY + 1);

        vm.startPrank(bob);
        governor.castVote(proposalId, uint8(IGovernor.Support.FOR), "");
        vm.expectRevert(abi.encodeWithSelector(IGovernor.AlreadyVoted.selector, proposalId, bob));
        governor.castVote(proposalId, uint8(IGovernor.Support.AGAINST), "");
        vm.stopPrank();
    }

    function test_votingBeforeTheDelayReverts() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);

        vm.prank(bob);
        vm.expectRevert();
        governor.castVote(proposalId, uint8(IGovernor.Support.FOR), "");
    }

    function test_votingAfterThePeriodReverts() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        skip(VOTING_DELAY + VOTING_PERIOD + 2);

        vm.prank(bob);
        vm.expectRevert();
        governor.castVote(proposalId, uint8(IGovernor.Support.FOR), "");
    }

    function test_invalidSupportReverts() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        skip(VOTING_DELAY + 1);

        vm.prank(bob);
        vm.expectRevert(abi.encodeWithSelector(IGovernor.InvalidSupport.selector, uint8(3)));
        governor.castVote(proposalId, 3, "");
    }

    // --- quorum and outcome ---

    /// A vote nobody attends should not pass, however lopsided it is.
    function test_proposalBelowQuorumIsDefeated() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 100); // 1%, under the 4% quorum

        uint256 proposalId = _propose(alice, 42);
        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));

        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.DEFEATED));
    }

    /// Abstentions reach quorum without supporting, which is what makes abstaining different from
    /// not voting at all.
    function test_abstentionsCountTowardQuorumButNotTheMargin() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 100);
        _fund(carol, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);

        skip(VOTING_DELAY + 1);
        vm.prank(bob);
        governor.castVote(proposalId, uint8(IGovernor.Support.FOR), "");
        vm.prank(carol);
        governor.castVote(proposalId, uint8(IGovernor.Support.ABSTAIN), "");
        skip(VOTING_PERIOD);

        assertEq(
            uint8(governor.state(proposalId)),
            uint8(IGovernor.ProposalState.SUCCEEDED),
            "abstentions should have carried it over quorum"
        );
        assertEq(governor.proposalOf(proposalId).forVotes, SUPPLY / 100, "and not into the margin");
    }

    function test_moreAgainstThanForIsDefeated() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);
        _fund(carol, SUPPLY / 5);

        uint256 proposalId = _propose(alice, 42);
        skip(VOTING_DELAY + 1);
        vm.prank(bob);
        governor.castVote(proposalId, uint8(IGovernor.Support.FOR), "");
        vm.prank(carol);
        governor.castVote(proposalId, uint8(IGovernor.Support.AGAINST), "");
        skip(VOTING_PERIOD);

        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.DEFEATED));
    }

    /// A tie fails. Passing on a tie would mean a proposal with no majority takes effect.
    function test_tieIsDefeated() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);
        _fund(carol, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        skip(VOTING_DELAY + 1);
        vm.prank(bob);
        governor.castVote(proposalId, uint8(IGovernor.Support.FOR), "");
        vm.prank(carol);
        governor.castVote(proposalId, uint8(IGovernor.Support.AGAINST), "");
        skip(VOTING_PERIOD);

        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.DEFEATED));
    }

    /// Quorum is a fraction of the supply at the snapshot, so it cannot move under a running vote.
    function test_quorumIsFixedAtTheSnapshot() public view {
        assertEq(governor.quorumNumerator(), QUORUM_NUMERATOR);
    }

    // --- proposal threshold ---

    function test_proposingBelowThresholdReverts() public {
        _fund(alice, PROPOSAL_THRESHOLD - 1);

        vm.prank(alice);
        vm.expectRevert(
            abi.encodeWithSelector(
                IGovernor.BelowProposalThreshold.selector, PROPOSAL_THRESHOLD - 1, PROPOSAL_THRESHOLD
            )
        );
        governor.propose(_localAction(42), "Set value", "");
    }

    function test_proposingRequiresATitle() public {
        _fund(alice, PROPOSAL_THRESHOLD);

        vm.prank(alice);
        vm.expectRevert(IGovernor.EmptyTitle.selector);
        governor.propose(_localAction(42), "", "");
    }

    // --- cancellation ---

    function test_proposerCanCancelTheirOwnProposal() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        uint256 proposalId = _propose(alice, 42);

        vm.prank(alice);
        governor.cancel(proposalId);

        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.CANCELLED));
    }

    /// The guardian exists for the case the timelock is meant to catch: a passed proposal nobody
    /// noticed was malicious. A guardian that could only act before the vote would be useless.
    function test_guardianCanCancelAQueuedProposal() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        governor.queue(proposalId);

        vm.prank(guardian);
        governor.cancel(proposalId);

        skip(TIMELOCK_DELAY + 1);
        vm.expectRevert();
        governor.execute(proposalId);
        assertEq(target.value(), 0, "a cancelled proposal executed");
    }

    function test_strangersCannotCancel() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        uint256 proposalId = _propose(alice, 42);

        vm.prank(carol);
        vm.expectRevert(abi.encodeWithSelector(IGovernor.NotProposerOrGuardian.selector, carol));
        governor.cancel(proposalId);
    }

    function test_executedProposalCannotBeCancelled() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        governor.queue(proposalId);
        skip(TIMELOCK_DELAY + 1);
        governor.execute(proposalId);

        vm.prank(guardian);
        vm.expectRevert();
        governor.cancel(proposalId);
    }

    // --- state machine ---

    function test_queueingAnUnsucceededProposalReverts() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        uint256 proposalId = _propose(alice, 42);

        vm.expectRevert();
        governor.queue(proposalId);
    }

    function test_executingAnUnqueuedProposalReverts() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));

        vm.expectRevert();
        governor.execute(proposalId);
    }

    function test_unknownProposalIsNone() public view {
        assertEq(uint8(governor.state(9999)), uint8(IGovernor.ProposalState.NONE));
    }

    function test_dispatchedStateIsUnreachableInPhaseOne() public {
        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        governor.queue(proposalId);
        skip(TIMELOCK_DELAY + 1);
        governor.execute(proposalId);

        assertTrue(
            governor.state(proposalId) != IGovernor.ProposalState.DISPATCHED,
            "nothing in Phase 1 should reach dispatched"
        );
    }
}
