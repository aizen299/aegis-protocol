// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {OracleFixture} from "../utils/OracleFixture.sol";
import {OracleStakingHandler} from "./OracleStakingHandler.sol";

contract OracleStakingInvariantTest is OracleFixture {
    OracleStakingHandler internal handler;

    function setUp() public override {
        super.setUp();

        handler = new OracleStakingHandler(staking, stakeToken, slasher);
        targetContract(address(handler));

        bytes4[] memory selectors = new bytes4[](7);
        selectors[0] = OracleStakingHandler.register.selector;
        selectors[1] = OracleStakingHandler.stake.selector;
        selectors[2] = OracleStakingHandler.requestUnstake.selector;
        selectors[3] = OracleStakingHandler.completeUnstake.selector;
        selectors[4] = OracleStakingHandler.cancelUnstake.selector;
        selectors[5] = OracleStakingHandler.slash.selector;
        selectors[6] = OracleStakingHandler.advanceTime.selector;
        targetSelector(FuzzSelector({addr: address(handler), selectors: selectors}));
    }

    /// Solvency: the contract always holds at least what it owes every node. Slashed stake stays
    /// in the contract, so the balance can exceed the sum of stakes but never fall short.
    function invariant_contractCoversEveryStake() public view {
        assertGe(stakeToken.balanceOf(address(staking)), handler.sumStakes());
    }

    /// No value is created: what left the contract never exceeds what went in, less what was
    /// slashed and is still held.
    function invariant_noValueCreation() public view {
        assertLe(handler.ghostWithdrawn(), handler.ghostStaked());
    }

    /// The cached counter cannot drift from the real active set — quorum is computed from it.
    function invariant_activeCountMatchesRealSet() public view {
        assertEq(staking.activeNodeCount(), handler.countActive());
    }

    /// A node that asked to leave never keeps participating, which is what makes the unbonding
    /// period meaningful rather than cosmetic.
    function invariant_noActiveNodeHasPendingUnstake() public view {
        assertFalse(handler.anyActiveWithPendingUnstake());
    }

    /// Slashing only ever reduces stake that is present; it can never exceed what was staked.
    function invariant_slashedNeverExceedsStaked() public view {
        assertLe(handler.ghostSlashed(), handler.ghostStaked());
    }

    /// Stake accounting is conserved: staked = held + withdrawn + slashed-and-retained.
    function invariant_stakeAccountingBalances() public view {
        assertEq(
            handler.ghostStaked(), handler.sumStakes() + handler.ghostWithdrawn() + handler.ghostSlashed()
        );
    }
}
