// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {CommonBase} from "forge-std/Base.sol";
import {StdCheats} from "forge-std/StdCheats.sol";
import {StdUtils} from "forge-std/StdUtils.sol";

import {OracleRounds} from "../../src/oracle/OracleRounds.sol";
import {OracleStaking} from "../../src/oracle/OracleStaking.sol";
import {IOracleRounds} from "../../src/oracle/interfaces/IOracleRounds.sol";

interface IERC20Minimal {
    function approve(
        address spender,
        uint256 amount
    ) external returns (bool);
}

contract OracleRoundsHandler is CommonBase, StdCheats, StdUtils {
    OracleRounds public immutable rounds;
    OracleStaking public immutable staking;
    address public immutable admin;
    bytes32 public immutable feedId;

    address[] public nodeAddrs;
    uint256[] public nodeKeys;

    uint256[] public settledRounds;
    uint256 public ghostOpenedCount;
    uint256 public ghostSubmittedCount;
    uint256 public ghostSettledCount;
    uint256 public ghostFailedCount;
    bool public ghostSettledBelowQuorum;
    bool public ghostSettledOutsideRange;

    constructor(
        OracleRounds rounds_,
        OracleStaking staking_,
        address admin_,
        bytes32 feedId_,
        address[] memory addrs,
        uint256[] memory keys
    ) {
        rounds = rounds_;
        staking = staking_;
        admin = admin_;
        feedId = feedId_;
        nodeAddrs = addrs;
        nodeKeys = keys;
    }

    function openRound(
        uint256
    ) external {
        uint256 current = rounds.currentRoundId(feedId);
        if (current != 0) {
            IOracleRounds.RoundState state = rounds.roundOf(current).state;
            if (state == IOracleRounds.RoundState.OPEN || state == IOracleRounds.RoundState.QUORUM_MET) {
                return;
            }
        }
        if (staking.activeNodeCount() < rounds.minQuorumNodes()) return;

        rounds.openRound(feedId);
        ghostOpenedCount += 1;
    }

    function submit(
        uint256 seed,
        uint256 value
    ) external {
        uint256 roundId = rounds.currentRoundId(feedId);
        if (roundId == 0) return;

        IOracleRounds.Round memory round = rounds.roundOf(roundId);
        if (
            round.state != IOracleRounds.RoundState.OPEN && round.state != IOracleRounds.RoundState.QUORUM_MET
        ) {
            return;
        }
        if (block.timestamp > round.deadline) return;

        // Walk from a fuzzed offset to the first eligible node that has not submitted. Picking a
        // single random index instead means repeated collisions, so quorum is rarely reached and
        // the suite never exercises settlement — the values and the interleaving are what need to
        // be random here, not which node speaks.
        (address node, uint256 index, bool found) = _nextSubmitter(roundId, round.nodeSetVersion, seed);
        if (!found) return;

        value = bound(value, 1, 1e30);
        uint256 nonce = rounds.nonceOf(node);
        bytes32 digest = rounds.submissionDigest(roundId, feedId, value, node, nonce);
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(nodeKeys[index], digest);

        vm.prank(node);
        rounds.submit(roundId, value, nonce, abi.encodePacked(r, s, v));
        ghostSubmittedCount += 1;
    }

    function _nextSubmitter(
        uint256 roundId,
        uint256 version,
        uint256 seed
    ) private view returns (address node, uint256 index, bool found) {
        uint256 count = nodeAddrs.length;
        // Reduce the seed before adding: the fuzzer supplies values near max uint256, and
        // `seed + i` overflows there, which silently turned this action into a no-op.
        uint256 base = seed % count;
        for (uint256 i = 0; i < count; i++) {
            uint256 candidate = (base + i) % count;
            address addr = nodeAddrs[candidate];
            if (!staking.isEligibleAt(addr, version)) continue;
            if (rounds.hasSubmitted(roundId, addr)) continue;
            return (addr, candidate, true);
        }
        return (address(0), 0, false);
    }

    function settle(
        uint256
    ) external {
        uint256 roundId = rounds.currentRoundId(feedId);
        if (roundId == 0) return;

        IOracleRounds.Round memory before = rounds.roundOf(roundId);
        if (
            before.state != IOracleRounds.RoundState.OPEN
                && before.state != IOracleRounds.RoundState.QUORUM_MET
        ) {
            return;
        }

        uint256[] memory values = rounds.submissionsOf(roundId);
        bool quorum = values.length >= rounds.minQuorumNodes()
            && values.length * 10_000 >= before.eligibleCount * rounds.quorumBps();
        if (!quorum && block.timestamp <= before.deadline) return;

        rounds.settleRound(roundId);

        IOracleRounds.Round memory settledRound = rounds.roundOf(roundId);
        if (settledRound.state == IOracleRounds.RoundState.SETTLED) {
            ghostSettledCount += 1;
            settledRounds.push(roundId);

            if (!quorum) ghostSettledBelowQuorum = true;
            if (!_withinRange(settledRound.aggregatedValue, values)) ghostSettledOutsideRange = true;
        } else {
            ghostFailedCount += 1;
        }
    }

    function deactivateNode(
        uint256 seed
    ) external {
        address node = nodeAddrs[seed % nodeAddrs.length];
        if (!staking.isActive(node)) return;
        // Floored at minQuorumNodes. Without the floor the set collapses to one node early in a
        // sequence, no round can open again, and every later action returns early — leaving the
        // suite vacuous rather than failing.
        if (staking.activeNodeCount() <= rounds.minQuorumNodes()) return;

        vm.prank(admin);
        staking.deactivate(node, "FUZZ");
    }

    function reactivateNode(
        uint256 seed
    ) external {
        address node = nodeAddrs[seed % nodeAddrs.length];
        if (staking.isActive(node)) return;
        if (staking.nodeInfo(node).pendingUnstake != 0) return;

        address token = staking.stakeToken();
        deal(token, node, 1e18);

        vm.startPrank(node);
        IERC20Minimal(token).approve(address(staking), 1e18);
        staking.stake(1e18);
        vm.stopPrank();
    }

    function advanceTime(
        uint256 seconds_
    ) external {
        // Bounded well under the 300s round duration on purpose: a single larger jump expires
        // every round before a submission can land, which starves the suite of settled rounds.
        skip(bound(seconds_, 1, 120));
    }

    function settledRoundCount() external view returns (uint256) {
        return settledRounds.length;
    }

    function settledRoundAt(
        uint256 i
    ) external view returns (uint256) {
        return settledRounds[i];
    }

    function _withinRange(
        uint256 value,
        uint256[] memory values
    ) private pure returns (bool) {
        if (values.length == 0) return false;

        uint256 low = values[0];
        uint256 high = values[0];
        for (uint256 i = 1; i < values.length; i++) {
            if (values[i] < low) low = values[i];
            if (values[i] > high) high = values[i];
        }
        return value >= low && value <= high;
    }
}
