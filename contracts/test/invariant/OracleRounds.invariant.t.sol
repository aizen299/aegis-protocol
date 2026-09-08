// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IOracleRounds} from "../../src/oracle/interfaces/IOracleRounds.sol";
import {OracleRoundsFixture} from "../utils/OracleRoundsFixture.sol";
import {OracleRoundsHandler} from "./OracleRoundsHandler.sol";

/// @dev Invariants about settled rounds prove nothing if no round ever settles, and every handler
///      action returning early would leave them trivially true. HandlerProbeTest guards that
///      deterministically — it drives the same handler through open, submit, and settle, and fails
///      if any of them is blocked.
contract OracleRoundsInvariantTest is OracleRoundsFixture {
    OracleRoundsHandler internal handler;

    function setUp() public override {
        super.setUp();
        _spawnNodes(5);

        address[] memory addrs = new address[](keyed.length);
        uint256[] memory keys = new uint256[](keyed.length);
        for (uint256 i = 0; i < keyed.length; i++) {
            addrs[i] = keyed[i].addr;
            keys[i] = keyed[i].key;
        }

        handler = new OracleRoundsHandler(rounds, staking, admin, FEED, addrs, keys);

        vm.prank(admin);
        staking.grantRole(0x00, address(handler));

        targetContract(address(handler));

        bytes4[] memory selectors = new bytes4[](6);
        selectors[0] = OracleRoundsHandler.openRound.selector;
        selectors[1] = OracleRoundsHandler.submit.selector;
        selectors[2] = OracleRoundsHandler.settle.selector;
        selectors[3] = OracleRoundsHandler.deactivateNode.selector;
        selectors[4] = OracleRoundsHandler.advanceTime.selector;
        selectors[5] = OracleRoundsHandler.reactivateNode.selector;
        targetSelector(FuzzSelector({addr: address(handler), selectors: selectors}));
    }

    /// No round ever settles on fewer submissions than quorum requires of its own snapshot,
    /// whatever the eligible set does afterwards.
    function invariant_neverSettlesBelowQuorum() public view {
        assertFalse(handler.ghostSettledBelowQuorum());
    }

    /// The settled value is always within the range the nodes actually reported. A value outside
    /// it would mean the aggregation produced a number nobody submitted.
    function invariant_settledValueIsWithinSubmittedRange() public view {
        assertFalse(handler.ghostSettledOutsideRange());
    }

    /// Settlement is terminal in both directions: a settled or failed round never reopens or
    /// changes its value.
    function invariant_settledRoundsAreTerminal() public view {
        uint256 count = handler.settledRoundCount();
        for (uint256 i = 0; i < count; i++) {
            IOracleRounds.Round memory round = rounds.roundOf(handler.settledRoundAt(i));
            assertEq(uint8(round.state), uint8(IOracleRounds.RoundState.SETTLED));
            assertGt(round.aggregatedValue, 0);
            assertGt(round.settledAt, 0);
        }
    }

    /// A feed has at most one live round: two would let the same nodes report twice into
    /// overlapping windows.
    function invariant_atMostOneLiveRoundPerFeed() public view {
        uint256 current = rounds.currentRoundId(FEED);
        if (current == 0) return;

        for (uint256 id = 1; id < current; id++) {
            IOracleRounds.Round memory round = rounds.roundOf(id);
            assertTrue(
                round.state == IOracleRounds.RoundState.SETTLED
                    || round.state == IOracleRounds.RoundState.FAILED,
                "an earlier round is still live"
            );
        }
    }

    /// A submission count above the frozen eligible count would mean ineligible nodes got in.
    function invariant_submissionsNeverExceedEligibleCount() public view {
        uint256 current = rounds.currentRoundId(FEED);
        for (uint256 id = 1; id <= current && id != 0; id++) {
            IOracleRounds.Round memory round = rounds.roundOf(id);
            if (round.openedAt == 0) continue;
            assertLe(rounds.submissionsOf(id).length, round.eligibleCount);
        }
    }

    /// The last settled round pointer only ever advances to a round that actually settled.
    function invariant_lastSettledPointsAtASettledRound() public view {
        uint256 last = rounds.lastSettledRoundId(FEED);
        if (last == 0) return;

        assertEq(uint8(rounds.roundOf(last).state), uint8(IOracleRounds.RoundState.SETTLED));
    }
}
