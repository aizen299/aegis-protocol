// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IOracleRounds} from "../../src/oracle/interfaces/IOracleRounds.sol";
import {OracleRoundsFixture} from "../utils/OracleRoundsFixture.sol";

contract OracleRoundsTest is OracleRoundsFixture {
    // --- the snapshot, and what it defends ---

    /// Quorum is 2/3 of the set that existed when the round opened. If the denominator were read
    /// live at settlement, nodes could deactivate mid-round to shrink the bar they have to clear —
    /// a round that should fail would settle on fewer submissions than policy intends.
    function test_deactivatingMidRoundDoesNotShrinkQuorum() public {
        _spawnNodes(6);
        uint256 roundId = rounds.openRound(FEED);
        assertEq(rounds.roundOf(roundId).eligibleCount, 6);

        // Three of six submit — short of the 2/3 of six that quorum requires.
        _submit(keyed[0], roundId, 3000e18);
        _submit(keyed[1], roundId, 3010e18);
        _submit(keyed[2], roundId, 2990e18);

        // The other three leave, hoping to make three-of-three a quorum.
        for (uint256 i = 3; i < 6; i++) {
            vm.prank(keyed[i].addr);
            staking.requestUnstake(MIN_STAKE);
        }
        assertEq(staking.activeNodeCount(), 3, "the live set really did shrink");

        skip(ROUND_DURATION + 1);
        rounds.settleRound(roundId);

        assertEq(
            uint8(rounds.roundOf(roundId).state),
            uint8(IOracleRounds.RoundState.FAILED),
            "the frozen denominator still requires 4 of 6"
        );
    }

    /// A node that joined after the round opened is not in the denominator, so it must not add to
    /// the numerator either.
    function test_lateJoinerCannotSubmit() public {
        _spawnNodes(3);
        uint256 roundId = rounds.openRound(FEED);

        (address latecomer, uint256 key) = makeAddrAndKey("latecomer");
        _registerNode(latecomer, MIN_STAKE);

        uint256 nonce = rounds.nonceOf(latecomer);
        bytes32 digest = rounds.submissionDigest(roundId, FEED, 3000e18, latecomer, nonce);
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(key, digest);

        // Hoisted for the same reason as the nonce: a call in argument position eats the prank.
        uint256 version = rounds.roundOf(roundId).nodeSetVersion;

        vm.prank(latecomer);
        vm.expectRevert(abi.encodeWithSelector(IOracleRounds.NotEligible.selector, latecomer, version));
        rounds.submit(roundId, 3000e18, nonce, abi.encodePacked(r, s, v));
    }

    function test_deactivatedNodeCannotSubmit() public {
        _spawnNodes(3);
        uint256 roundId = rounds.openRound(FEED);

        vm.prank(admin);
        staking.deactivate(keyed[0].addr, "MANUAL");

        bytes memory signature = _sign(keyed[0], roundId, 3000e18);
        uint256 nonce = rounds.nonceOf(keyed[0].addr);
        vm.prank(keyed[0].addr);
        vm.expectRevert();
        rounds.submit(roundId, 3000e18, nonce, signature);
    }

    function test_openRoundRequiresMinimumEligibleNodes() public {
        _spawnNodes(2);

        vm.expectRevert(
            abi.encodeWithSelector(IOracleRounds.NotEnoughEligibleNodes.selector, 2, MIN_QUORUM_NODES)
        );
        rounds.openRound(FEED);
    }

    // --- lifecycle ---

    function test_roundSettlesWithMedianOfOddSubmissions() public {
        _spawnNodes(3);
        uint256 roundId = rounds.openRound(FEED);

        _submit(keyed[0], roundId, 3100e18);
        _submit(keyed[1], roundId, 2900e18);
        _submit(keyed[2], roundId, 3000e18);

        rounds.settleRound(roundId);

        IOracleRounds.Round memory round = rounds.roundOf(roundId);
        assertEq(uint8(round.state), uint8(IOracleRounds.RoundState.SETTLED));
        assertEq(round.aggregatedValue, 3000e18, "median, not mean: an outlier must not move it");
    }

    function test_roundSettlesWithMeanOfTwoMiddleSubmissions() public {
        _spawnNodes(4);
        uint256 roundId = rounds.openRound(FEED);

        _submit(keyed[0], roundId, 1000e18);
        _submit(keyed[1], roundId, 2000e18);
        _submit(keyed[2], roundId, 3000e18);
        _submit(keyed[3], roundId, 4000e18);

        rounds.settleRound(roundId);
        assertEq(rounds.roundOf(roundId).aggregatedValue, 2500e18);
    }

    /// A single wild submission must not move the settled value — that is the whole point of a
    /// median over a mean.
    function test_singleOutlierDoesNotMoveTheMedian() public {
        _spawnNodes(5);
        uint256 roundId = rounds.openRound(FEED);

        _submit(keyed[0], roundId, 3000e18);
        _submit(keyed[1], roundId, 3001e18);
        _submit(keyed[2], roundId, 2999e18);
        _submit(keyed[3], roundId, 3002e18);
        _submit(keyed[4], roundId, 1); // manipulated

        rounds.settleRound(roundId);
        assertEq(rounds.roundOf(roundId).aggregatedValue, 3000e18);
    }

    function test_quorumMetTransitionEmitsAndSettlesEarly() public {
        _spawnNodes(3);
        uint256 roundId = rounds.openRound(FEED);

        _submit(keyed[0], roundId, 3000e18);
        assertEq(uint8(rounds.roundOf(roundId).state), uint8(IOracleRounds.RoundState.OPEN));

        _submit(keyed[1], roundId, 3000e18);
        _submit(keyed[2], roundId, 3000e18);
        assertEq(uint8(rounds.roundOf(roundId).state), uint8(IOracleRounds.RoundState.QUORUM_MET));

        // Quorum reached, so settlement need not wait for the deadline.
        rounds.settleRound(roundId);
        assertEq(uint8(rounds.roundOf(roundId).state), uint8(IOracleRounds.RoundState.SETTLED));
    }

    function test_settleBeforeDeadlineWithoutQuorumReverts() public {
        _spawnNodes(6);
        uint256 roundId = rounds.openRound(FEED);
        _submit(keyed[0], roundId, 3000e18);

        vm.expectRevert(
            abi.encodeWithSelector(
                IOracleRounds.RoundStillOpen.selector, roundId, rounds.roundOf(roundId).deadline
            )
        );
        rounds.settleRound(roundId);
    }

    function test_roundFailsWhenQuorumNeverReached() public {
        _spawnNodes(6);
        uint256 roundId = rounds.openRound(FEED);
        _submit(keyed[0], roundId, 3000e18);

        skip(ROUND_DURATION + 1);
        rounds.settleRound(roundId);

        assertEq(uint8(rounds.roundOf(roundId).state), uint8(IOracleRounds.RoundState.FAILED));
        assertEq(rounds.lastSettledRoundId(FEED), 0, "a failed round is not a price");
    }

    function test_submissionAfterDeadlineReverts() public {
        _spawnNodes(3);
        uint256 roundId = rounds.openRound(FEED);
        skip(ROUND_DURATION + 1);

        bytes memory signature = _sign(keyed[0], roundId, 3000e18);
        uint256 nonce = rounds.nonceOf(keyed[0].addr);
        vm.prank(keyed[0].addr);
        vm.expectRevert(abi.encodeWithSelector(IOracleRounds.RoundNotOpen.selector, roundId));
        rounds.submit(roundId, 3000e18, nonce, signature);
    }

    function test_cannotOpenSecondRoundWhileOneIsLive() public {
        _spawnNodes(3);
        uint256 roundId = rounds.openRound(FEED);

        vm.expectRevert(abi.encodeWithSelector(IOracleRounds.RoundAlreadyOpen.selector, FEED, roundId));
        rounds.openRound(FEED);
    }

    function test_nextRoundOpensAfterSettlement() public {
        _spawnNodes(3);
        uint256 first = rounds.openRound(FEED);
        _submit(keyed[0], first, 3000e18);
        _submit(keyed[1], first, 3000e18);
        _submit(keyed[2], first, 3000e18);
        rounds.settleRound(first);

        uint256 second = rounds.openRound(FEED);
        assertEq(second, first + 1);
    }

    // --- signatures ---

    function test_signatureFromAnotherKeyReverts() public {
        _spawnNodes(3);
        uint256 roundId = rounds.openRound(FEED);

        uint256 nonce = rounds.nonceOf(keyed[0].addr);
        bytes32 digest = rounds.submissionDigest(roundId, FEED, 3000e18, keyed[0].addr, nonce);
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(keyed[1].key, digest);

        vm.prank(keyed[0].addr);
        vm.expectRevert(abi.encodeWithSelector(IOracleRounds.InvalidSignature.selector, keyed[0].addr));
        rounds.submit(roundId, 3000e18, nonce, abi.encodePacked(r, s, v));
    }

    /// The signed value is bound to the payload: signing 3000 and submitting 9000 must not verify.
    function test_signatureIsBoundToTheSubmittedValue() public {
        _spawnNodes(3);
        uint256 roundId = rounds.openRound(FEED);

        bytes memory signature = _sign(keyed[0], roundId, 3000e18);
        uint256 nonce = rounds.nonceOf(keyed[0].addr);
        vm.prank(keyed[0].addr);
        vm.expectRevert(abi.encodeWithSelector(IOracleRounds.InvalidSignature.selector, keyed[0].addr));
        rounds.submit(roundId, 9000e18, nonce, signature);
    }

    /// A signature for one round cannot be replayed into the next.
    function test_signatureCannotBeReplayedAcrossRounds() public {
        _spawnNodes(3);
        uint256 first = rounds.openRound(FEED);
        bytes memory signature = _sign(keyed[0], first, 3000e18);
        uint256 nonce = rounds.nonceOf(keyed[0].addr);

        _submit(keyed[0], first, 3000e18);
        _submit(keyed[1], first, 3000e18);
        _submit(keyed[2], first, 3000e18);
        rounds.settleRound(first);

        uint256 second = rounds.openRound(FEED);
        vm.prank(keyed[0].addr);
        vm.expectRevert();
        rounds.submit(second, 3000e18, nonce, signature);
    }

    function test_doubleSubmissionReverts() public {
        _spawnNodes(3);
        uint256 roundId = rounds.openRound(FEED);
        _submit(keyed[0], roundId, 3000e18);

        bytes memory signature = _sign(keyed[0], roundId, 3050e18);
        uint256 nonce = rounds.nonceOf(keyed[0].addr);
        vm.prank(keyed[0].addr);
        vm.expectRevert(
            abi.encodeWithSelector(IOracleRounds.AlreadySubmitted.selector, roundId, keyed[0].addr)
        );
        rounds.submit(roundId, 3050e18, nonce, signature);
    }

    function test_wrongNonceReverts() public {
        _spawnNodes(3);
        uint256 roundId = rounds.openRound(FEED);

        uint256 nonce = rounds.nonceOf(keyed[0].addr);
        bytes32 digest = rounds.submissionDigest(roundId, FEED, 3000e18, keyed[0].addr, nonce + 5);
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(keyed[0].key, digest);

        vm.prank(keyed[0].addr);
        vm.expectRevert(abi.encodeWithSelector(IOracleRounds.InvalidNonce.selector, nonce + 5, nonce));
        rounds.submit(roundId, 3000e18, nonce + 5, abi.encodePacked(r, s, v));
    }

    // --- reader ---

    /// The classic oracle exploit is consuming a value that looks fine and is hours old, so the
    /// reader has no default freshness and fails closed.
    function test_readerRevertsOnStaleValue() public {
        _spawnNodes(3);
        uint256 roundId = rounds.openRound(FEED);
        _submit(keyed[0], roundId, 3000e18);
        _submit(keyed[1], roundId, 3000e18);
        _submit(keyed[2], roundId, 3000e18);
        rounds.settleRound(roundId);

        (uint256 value,,) = rounds.getValue(FEED, 1 hours);
        assertEq(value, 3000e18);

        skip(2 hours);
        vm.expectRevert();
        rounds.getValue(FEED, 1 hours);
    }

    function test_readerRevertsWhenNothingSettled() public {
        vm.expectRevert(abi.encodeWithSelector(IOracleRounds.NoSettledRound.selector, FEED));
        rounds.getValue(FEED, 1 hours);
    }

    function test_readerRevertsForUnknownFeed() public {
        bytes32 unknown = keccak256("BTC/USD");
        vm.expectRevert(abi.encodeWithSelector(IOracleRounds.FeedNotRegistered.selector, unknown));
        rounds.getValue(unknown, 1 hours);
    }

    // --- admin ---

    function test_openRoundRequiresRegisteredFeed() public {
        _spawnNodes(3);
        bytes32 unknown = keccak256("BTC/USD");

        vm.expectRevert(abi.encodeWithSelector(IOracleRounds.FeedNotRegistered.selector, unknown));
        rounds.openRound(unknown);
    }

    /// The scale is declared at registration and readable from chain, so nothing downstream has to
    /// assume it. The contract cannot enforce it — it medians whatever nodes submit — but one
    /// declared value beats every consumer guessing independently.
    function test_feedDecimalsAreReadableFromChain() public {
        assertEq(rounds.feedDecimals(FEED), 18);

        bytes32 rateFeed = keccak256("SOFR");
        vm.prank(oracleManager);
        rounds.registerFeed(rateFeed, "SOFR", 8);

        assertEq(rounds.feedDecimals(rateFeed), 8, "a feed may declare a scale other than 18");
    }

    function test_registerFeedRejectsImpossibleDecimals() public {
        vm.prank(oracleManager);
        vm.expectRevert(abi.encodeWithSelector(IOracleRounds.InvalidDecimals.selector, uint8(39)));
        rounds.registerFeed(keccak256("BAD"), "BAD", 39);
    }

    function test_feedDecimalsRevertsForUnknownFeed() public {
        bytes32 unknown = keccak256("NOPE");
        vm.expectRevert(abi.encodeWithSelector(IOracleRounds.FeedNotRegistered.selector, unknown));
        rounds.feedDecimals(unknown);
    }

    function test_registerFeedRequiresManagerRole() public {
        vm.prank(outsider);
        vm.expectRevert();
        rounds.registerFeed(keccak256("BTC/USD"), "BTC/USD", 18);
    }

    function test_pauseHaltsSubmissions() public {
        _spawnNodes(3);
        uint256 roundId = rounds.openRound(FEED);

        vm.prank(admin);
        rounds.pause();

        bytes memory signature = _sign(keyed[0], roundId, 3000e18);
        uint256 nonce = rounds.nonceOf(keyed[0].addr);
        vm.prank(keyed[0].addr);
        vm.expectRevert();
        rounds.submit(roundId, 3000e18, nonce, signature);
    }

    function test_zeroValueSubmissionReverts() public {
        _spawnNodes(3);
        uint256 roundId = rounds.openRound(FEED);

        uint256 nonce = rounds.nonceOf(keyed[0].addr);
        bytes32 digest = rounds.submissionDigest(roundId, FEED, 0, keyed[0].addr, nonce);
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(keyed[0].key, digest);

        vm.prank(keyed[0].addr);
        vm.expectRevert(IOracleRounds.ZeroValue.selector);
        rounds.submit(roundId, 0, nonce, abi.encodePacked(r, s, v));
    }
}
