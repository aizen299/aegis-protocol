// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {CommonBase} from "forge-std/Base.sol";
import {StdCheats} from "forge-std/StdCheats.sol";
import {StdUtils} from "forge-std/StdUtils.sol";

import {AegisToken} from "../../src/governance/AegisToken.sol";
import {Governor} from "../../src/governance/Governor.sol";
import {Timelock} from "../../src/governance/Timelock.sol";
import {IGovernor} from "../../src/governance/interfaces/IGovernor.sol";
import {GovernedTarget} from "../utils/GovernorFixture.sol";

/// @dev Every action returns early rather than reverting: `fail_on_revert = true` is on, and a
///      handler that reverts has stopped exercising the contract. The cases that must be observed
///      failing — a remote destination, an early execution — go through try/catch so the revert is
///      recorded rather than swallowed by a guard.
contract GovernanceHandler is CommonBase, StdCheats, StdUtils {
    AegisToken public immutable token;
    Governor public immutable governor;
    Timelock public immutable timelock;
    GovernedTarget public immutable target;
    address public immutable guardian;

    address[] public actors;

    uint256[] public proposalIds;
    mapping(uint256 proposalId => bool remote) public isRemote;
    mapping(uint256 proposalId => address proposer) public proposerOf;
    uint256[] public cancelledIds;
    uint256[] public executedIds;

    uint256 public ghostProposedCount;
    uint256 public ghostVoteCount;
    uint256 public ghostQueuedCount;
    uint256 public ghostExecutedCount;
    uint256 public ghostCancelledCount;

    bool public ghostDoubleVoted;
    bool public ghostExecutedTwice;
    bool public ghostExecutedEarly;
    bool public ghostRemoteCallLandedLocally;
    bool public ghostWeightDivergedFromSnapshot;

    constructor(
        AegisToken token_,
        Governor governor_,
        Timelock timelock_,
        GovernedTarget target_,
        address guardian_,
        address[] memory actors_
    ) {
        token = token_;
        governor = governor_;
        timelock = timelock_;
        target = target_;
        guardian = guardian_;
        actors = actors_;
    }

    // --- actions ---

    function propose(
        uint256 actorSeed,
        uint256 newValue,
        bool remote
    ) external {
        address actor = _actor(actorSeed);
        if (token.getVotes(actor) < governor.proposalThreshold()) return;

        IGovernor.Action memory action = IGovernor.Action({
            targetChainId: remote ? block.chainid + 1 : block.chainid,
            target: bytes32(uint256(uint160(address(target)))),
            value: 0,
            payload: abi.encodeCall(GovernedTarget.setValue, (bound(newValue, 1, type(uint128).max)))
        });

        vm.prank(actor);
        uint256 proposalId = governor.propose(action, "fuzz", "");

        proposalIds.push(proposalId);
        isRemote[proposalId] = remote;
        proposerOf[proposalId] = actor;
        ghostProposedCount += 1;
    }

    function castVote(
        uint256 proposalSeed,
        uint256 actorSeed,
        uint8 support
    ) external {
        (bool found, uint256 proposalId) = _proposal(proposalSeed);
        if (!found) return;
        if (governor.state(proposalId) != IGovernor.ProposalState.ACTIVE) return;

        address actor = _actor(actorSeed);
        if (governor.hasVoted(proposalId, actor)) {
            // A second vote must be refused, not merely unlikely. Observed, not guarded around.
            vm.prank(actor);
            try governor.castVote(proposalId, uint8(bound(support, 0, 2)), "") {
                ghostDoubleVoted = true;
            } catch {}
            return;
        }

        uint48 voteStart = governor.proposalOf(proposalId).voteStart;
        uint256 snapshotWeight = token.getPastVotes(actor, voteStart);
        if (snapshotWeight == 0) return;

        vm.prank(actor);
        governor.castVote(proposalId, uint8(bound(support, 0, 2)), "");

        // The flash-loan property: what was counted is the snapshot's weight, not today's.
        if (token.getPastVotes(actor, voteStart) != snapshotWeight) {
            ghostWeightDivergedFromSnapshot = true;
        }
        ghostVoteCount += 1;
    }

    function queue(
        uint256 proposalSeed
    ) external {
        (bool found, uint256 proposalId) = _proposal(proposalSeed);
        if (!found) return;
        if (governor.state(proposalId) != IGovernor.ProposalState.SUCCEEDED) return;

        governor.queue(proposalId);
        ghostQueuedCount += 1;
    }

    function execute(
        uint256 proposalSeed
    ) external {
        (bool found, uint256 proposalId) = _proposal(proposalSeed);
        if (!found) return;
        if (governor.state(proposalId) != IGovernor.ProposalState.QUEUED) return;

        uint48 executableAt = governor.proposalOf(proposalId).executableAt;
        bool elapsed = block.timestamp >= executableAt;
        uint256 valueBefore = target.value();

        try governor.execute(proposalId) {
            if (!elapsed) ghostExecutedEarly = true;
            if (isRemote[proposalId]) ghostRemoteCallLandedLocally = true;
            executedIds.push(proposalId);
            ghostExecutedCount += 1;

            // A second execution must be refused.
            try governor.execute(proposalId) {
                ghostExecutedTwice = true;
            } catch {}
        } catch {
            // A refused execution must not have moved the target.
            if (target.value() != valueBefore) ghostRemoteCallLandedLocally = true;
        }
    }

    function cancel(
        uint256 proposalSeed,
        bool asGuardian
    ) external {
        (bool found, uint256 proposalId) = _proposal(proposalSeed);
        if (!found) return;

        IGovernor.ProposalState current = governor.state(proposalId);
        if (
            current == IGovernor.ProposalState.EXECUTED || current == IGovernor.ProposalState.CANCELLED
                || current == IGovernor.ProposalState.NONE
        ) {
            return;
        }

        vm.prank(asGuardian ? guardian : proposerOf[proposalId]);
        governor.cancel(proposalId);

        cancelledIds.push(proposalId);
        ghostCancelledCount += 1;
    }

    /// @dev Moving weight after a snapshot is the whole point of taking one.
    function moveWeight(
        uint256 fromSeed,
        uint256 toSeed,
        uint256 amount
    ) external {
        address from = _actor(fromSeed);
        address to = _actor(toSeed);
        if (from == to) return;

        uint256 balance = token.balanceOf(from);
        if (balance == 0) return;

        vm.prank(from);
        token.transfer(to, bound(amount, 1, balance));

        vm.prank(to);
        token.delegate(to);
    }

    function advanceTime(
        uint256 seed
    ) external {
        vm.warp(block.timestamp + bound(seed, 1 hours, 3 days));
    }

    // --- views ---

    function proposalCount() external view returns (uint256) {
        return proposalIds.length;
    }

    function proposalAt(
        uint256 i
    ) external view returns (uint256) {
        return proposalIds[i];
    }

    function cancelledCount() external view returns (uint256) {
        return cancelledIds.length;
    }

    function cancelledAt(
        uint256 i
    ) external view returns (uint256) {
        return cancelledIds[i];
    }

    function executedCount() external view returns (uint256) {
        return executedIds.length;
    }

    function executedAt(
        uint256 i
    ) external view returns (uint256) {
        return executedIds[i];
    }

    // --- internals ---

    function _actor(
        uint256 seed
    ) private view returns (address) {
        return actors[seed % actors.length];
    }

    function _proposal(
        uint256 seed
    ) private view returns (bool, uint256) {
        if (proposalIds.length == 0) return (false, 0);
        return (true, proposalIds[seed % proposalIds.length]);
    }
}
