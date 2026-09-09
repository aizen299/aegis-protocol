// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IGovernor} from "../../src/governance/interfaces/IGovernor.sol";
import {ITimelock} from "../../src/governance/interfaces/ITimelock.sol";
import {GovernorFixture} from "../utils/GovernorFixture.sol";
import {GovernanceHandler} from "./GovernanceHandler.sol";

/// @dev The invariants in docs/v0.3-governance-plan.md §6, enforced. Every one of them is vacuous
///      if no proposal ever executes, so GovernanceProbeTest drives the same handler
///      deterministically through the whole lifecycle and fails if any step is blocked.
contract GovernanceInvariantTest is GovernorFixture {
    GovernanceHandler internal handler;

    function setUp() public override {
        super.setUp();

        address[] memory actors = new address[](3);
        actors[0] = alice;
        actors[1] = bob;
        actors[2] = carol;
        for (uint256 i = 0; i < actors.length; i++) {
            _fund(actors[i], SUPPLY / 5);
        }

        handler = new GovernanceHandler(token, governor, timelock, target, guardian, actors);

        targetContract(address(handler));

        bytes4[] memory selectors = new bytes4[](7);
        selectors[0] = GovernanceHandler.propose.selector;
        selectors[1] = GovernanceHandler.castVote.selector;
        selectors[2] = GovernanceHandler.queue.selector;
        selectors[3] = GovernanceHandler.execute.selector;
        selectors[4] = GovernanceHandler.cancel.selector;
        selectors[5] = GovernanceHandler.moveWeight.selector;
        selectors[6] = GovernanceHandler.advanceTime.selector;
        targetSelector(FuzzSelector({addr: address(handler), selectors: selectors}));
    }

    /// Vote totals never exceed the supply that existed at the snapshot. Exceeding it would mean
    /// weight was counted that no balance backed.
    function invariant_voteTotalsNeverExceedSnapshotSupply() public view {
        uint256 count = handler.proposalCount();
        for (uint256 i = 0; i < count; i++) {
            IGovernor.Proposal memory proposal = governor.proposalOf(handler.proposalAt(i));
            if (block.timestamp <= proposal.voteStart) continue;

            uint256 cast = proposal.forVotes + proposal.againstVotes + proposal.abstainVotes;
            assertLe(cast, token.getPastTotalSupply(proposal.voteStart), "weight exceeded supply");
        }
    }

    /// One vote per account per proposal. The handler tries a second vote whenever it can and
    /// records it if one is ever accepted.
    function invariant_oneVotePerAccountPerProposal() public view {
        assertFalse(handler.ghostDoubleVoted(), "an account voted twice");
    }

    /// Counted weight comes from the snapshot, so it cannot move after the fact — the property the
    /// flash-loan defence rests on, checked while the handler shuffles balances around.
    function invariant_countedWeightIsFixedAtTheSnapshot() public view {
        assertFalse(handler.ghostWeightDivergedFromSnapshot(), "snapshot weight moved");
    }

    /// A proposal cannot be executed before its timelock elapses.
    function invariant_executionNeverPrecedesTheTimelock() public view {
        assertFalse(handler.ghostExecutedEarly(), "an action ran before its delay elapsed");
    }

    /// A proposal cannot be executed twice.
    function invariant_noProposalExecutesTwice() public view {
        assertFalse(handler.ghostExecutedTwice(), "a proposal executed twice");
    }

    /// A cancelled proposal can never reach executed, in either contract.
    function invariant_cancelledNeverReachesExecuted() public view {
        uint256 count = handler.cancelledCount();
        for (uint256 i = 0; i < count; i++) {
            uint256 proposalId = handler.cancelledAt(i);
            assertEq(
                uint8(governor.state(proposalId)),
                uint8(IGovernor.ProposalState.CANCELLED),
                "a cancelled proposal changed state"
            );

            uint256 operationId = governor.proposalOf(proposalId).operationId;
            if (operationId == 0) continue;
            assertTrue(
                timelock.operationOf(operationId).state != ITimelock.OperationState.EXECUTED,
                "a cancelled proposal's operation executed"
            );
        }
    }

    /// A proposal whose targetChainId is not the local chain never performs a local call.
    function invariant_remoteProposalNeverPerformsALocalCall() public view {
        assertFalse(handler.ghostRemoteCallLandedLocally(), "a remote action landed locally");
    }

    /// Only a queued proposal executes: an executed one always carries the operation that ran it,
    /// and that operation is executed in the timelock too. The two records cannot disagree.
    function invariant_executedProposalsMatchExecutedOperations() public view {
        uint256 count = handler.executedCount();
        for (uint256 i = 0; i < count; i++) {
            uint256 proposalId = handler.executedAt(i);
            assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.EXECUTED));

            uint256 operationId = governor.proposalOf(proposalId).operationId;
            assertGt(operationId, 0, "an executed proposal carried no operation");
            assertEq(
                uint8(timelock.operationOf(operationId).state),
                uint8(ITimelock.OperationState.EXECUTED),
                "the governor and the timelock disagree"
            );
        }
    }

    /// The timelock never holds more operations than proposals that reached queued. A surplus
    /// would mean something scheduled an action without a vote behind it.
    function invariant_everyOperationCameFromAProposal() public view {
        assertLe(timelock.operationCount(), handler.ghostQueuedCount());
    }
}
