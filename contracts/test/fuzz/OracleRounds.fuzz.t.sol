// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IOracleRounds} from "../../src/oracle/interfaces/IOracleRounds.sol";
import {OracleRoundsFixture} from "../utils/OracleRoundsFixture.sol";

contract OracleRoundsFuzzTest is OracleRoundsFixture {
    uint256 internal constant MIN_PRICE = 1;
    uint256 internal constant MAX_PRICE = 1e30;

    /// The settled value must be a value some node actually submitted, or the mean of the two
    /// middle ones. Anything else means the aggregation invented a number.
    function testFuzz_settledValueComesFromSubmissions(
        uint256 a,
        uint256 b,
        uint256 c
    ) public {
        uint256[3] memory values =
            [bound(a, MIN_PRICE, MAX_PRICE), bound(b, MIN_PRICE, MAX_PRICE), bound(c, MIN_PRICE, MAX_PRICE)];

        _spawnNodes(3);
        uint256 roundId = rounds.openRound(FEED);
        for (uint256 i = 0; i < 3; i++) {
            _submit(keyed[i], roundId, values[i]);
        }
        rounds.settleRound(roundId);

        uint256 settled = rounds.roundOf(roundId).aggregatedValue;
        assertTrue(
            settled == values[0] || settled == values[1] || settled == values[2],
            "an odd-sized median must be one of the submitted values"
        );
    }

    /// The median always lies between the smallest and largest submission — it can never be
    /// dragged outside the range the nodes reported.
    function testFuzz_settledValueLiesWithinSubmittedRange(
        uint256 seed,
        uint8 count
    ) public {
        uint256 n = bound(count, 3, 7);
        _spawnNodes(n);
        uint256 roundId = rounds.openRound(FEED);

        uint256 low = type(uint256).max;
        uint256 high = 0;
        for (uint256 i = 0; i < n; i++) {
            uint256 value = bound(uint256(keccak256(abi.encode(seed, i))), MIN_PRICE, MAX_PRICE);
            if (value < low) low = value;
            if (value > high) high = value;
            _submit(keyed[i], roundId, value);
        }
        rounds.settleRound(roundId);

        uint256 settled = rounds.roundOf(roundId).aggregatedValue;
        assertGe(settled, low);
        assertLe(settled, high);
    }

    /// One node reporting anything at all must not move the settled value away from what the
    /// honest majority reported. This is the property that makes medianization worth its gas.
    function testFuzz_oneNodeCannotMoveTheMedianMaterially(
        uint256 honest,
        uint256 manipulated
    ) public {
        honest = bound(honest, 1e18, 1e24);
        manipulated = bound(manipulated, MIN_PRICE, MAX_PRICE);

        _spawnNodes(5);
        uint256 roundId = rounds.openRound(FEED);

        // Four honest nodes cluster tightly; the fifth reports an arbitrary value.
        _submit(keyed[0], roundId, honest);
        _submit(keyed[1], roundId, honest);
        _submit(keyed[2], roundId, honest);
        _submit(keyed[3], roundId, honest);
        _submit(keyed[4], roundId, manipulated);

        rounds.settleRound(roundId);
        assertEq(
            rounds.roundOf(roundId).aggregatedValue,
            honest,
            "a single node moved the median away from the honest cluster"
        );
    }

    /// Submission order must not change the outcome — the sort has to be total, not incidental.
    function testFuzz_medianIsOrderIndependent(
        uint256 a,
        uint256 b,
        uint256 c
    ) public {
        uint256 x = bound(a, MIN_PRICE, MAX_PRICE);
        uint256 y = bound(b, MIN_PRICE, MAX_PRICE);
        uint256 z = bound(c, MIN_PRICE, MAX_PRICE);

        _spawnNodes(3);

        uint256 first = rounds.openRound(FEED);
        _submit(keyed[0], first, x);
        _submit(keyed[1], first, y);
        _submit(keyed[2], first, z);
        rounds.settleRound(first);
        uint256 forwards = rounds.roundOf(first).aggregatedValue;

        uint256 second = rounds.openRound(FEED);
        _submit(keyed[0], second, z);
        _submit(keyed[1], second, y);
        _submit(keyed[2], second, x);
        rounds.settleRound(second);

        assertEq(rounds.roundOf(second).aggregatedValue, forwards);
    }

    /// A round can never settle on fewer submissions than quorum requires of the set frozen at
    /// open, however the eligible set moves afterwards.
    function testFuzz_settlementRequiresQuorumOfTheSnapshot(
        uint8 nodeCount,
        uint8 submitCount
    ) public {
        uint256 n = bound(nodeCount, 3, 8);
        uint256 submissions = bound(submitCount, 1, n);

        _spawnNodes(n);
        uint256 roundId = rounds.openRound(FEED);
        uint256 eligible = rounds.roundOf(roundId).eligibleCount;

        for (uint256 i = 0; i < submissions; i++) {
            _submit(keyed[i], roundId, 3000e18);
        }

        skip(ROUND_DURATION + 1);
        rounds.settleRound(roundId);

        bool quorumReached = submissions >= MIN_QUORUM_NODES && submissions * 10_000 >= eligible * QUORUM_BPS;
        IOracleRounds.RoundState expected =
            quorumReached ? IOracleRounds.RoundState.SETTLED : IOracleRounds.RoundState.FAILED;

        assertEq(uint8(rounds.roundOf(roundId).state), uint8(expected));
    }

    /// Nonces advance by exactly one per accepted submission, so a replayed signature can never
    /// line up with the expected nonce again.
    function testFuzz_nonceAdvancesOncePerSubmission(
        uint8 roundCount
    ) public {
        uint256 n = bound(roundCount, 1, 5);
        _spawnNodes(3);

        for (uint256 r = 0; r < n; r++) {
            uint256 roundId = rounds.openRound(FEED);
            assertEq(rounds.nonceOf(keyed[0].addr), r, "nonce must equal the number of submissions so far");

            _submit(keyed[0], roundId, 3000e18);
            _submit(keyed[1], roundId, 3000e18);
            _submit(keyed[2], roundId, 3000e18);
            rounds.settleRound(roundId);
        }

        assertEq(rounds.nonceOf(keyed[0].addr), n);
    }
}
