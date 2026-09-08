// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {OracleRoundsFixture} from "../utils/OracleRoundsFixture.sol";
import {OracleRoundsHandler} from "./OracleRoundsHandler.sol";

/// @dev Not an invariant suite: drives the handler deterministically so a starved fuzz run can be
///      diagnosed instead of guessed at.
contract HandlerProbeTest is OracleRoundsFixture {
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
    }

    function test_handlerCanDeactivateAndReactivate() public {
        handler.deactivateNode(0);
        assertEq(staking.activeNodeCount(), 4, "deactivate did not take effect");

        handler.reactivateNode(0);
        assertEq(staking.activeNodeCount(), 5, "reactivate did not take effect");
    }

    function test_handlerCanOpenSubmitAndSettle() public {
        handler.openRound(0);
        assertEq(handler.ghostOpenedCount(), 1, "openRound was blocked");

        for (uint256 i = 0; i < 5; i++) {
            handler.submit(i, 3000e18);
        }
        assertEq(handler.ghostSubmittedCount(), 5, "submit was blocked");

        handler.settle(0);
        assertEq(handler.ghostSettledCount(), 1, "settle was blocked");
    }
}
