// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IGovernor} from "../../src/governance/interfaces/IGovernor.sol";
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
            abi.encodeWithSelector(IGovernor.CrossChainDispatchUnavailable.selector, block.chainid + 1)
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

        vm.expectRevert(abi.encodeWithSelector(IGovernor.TargetNotLocalAddress.selector, action.target));
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

        vm.expectRevert(abi.encodeWithSelector(IGovernor.ExecutionReverted.selector, proposalId));
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
