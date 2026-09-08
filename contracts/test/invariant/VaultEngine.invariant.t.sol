// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {Roles} from "../../src/shared/access/Roles.sol";
import {VaultFixture} from "../utils/VaultFixture.sol";
import {VaultHandler} from "./VaultHandler.sol";

contract VaultEngineInvariantTest is VaultFixture {
    VaultHandler internal handler;

    function setUp() public override {
        super.setUp();

        vm.prank(manager);
        vault.setStrategy(address(strategy));

        handler = new VaultHandler(vault, token, strategy, manager);
        targetContract(address(handler));

        bytes4[] memory selectors = new bytes4[](5);
        selectors[0] = VaultHandler.deposit.selector;
        selectors[1] = VaultHandler.withdraw.selector;
        selectors[2] = VaultHandler.allocate.selector;
        selectors[3] = VaultHandler.deallocate.selector;
        selectors[4] = VaultHandler.accrueYield.selector;
        targetSelector(FuzzSelector({addr: address(handler), selectors: selectors}));
    }

    /// @dev Solvency: the assets owed to every outstanding share never exceed the assets held.
    function invariant_sharesSolvency() public view {
        assertLe(vault.convertToAssets(vault.totalShares()), vault.totalAssets());
    }

    /// @dev Share supply is fully attributable to the known actors — no shares appear from nowhere.
    function invariant_shareSupplyMatchesHolders() public view {
        assertEq(handler.sumActorShares(), vault.totalShares());
    }

    /// @dev Assets are either idle in the vault or held by the strategy. Nothing else.
    function invariant_assetsAccountedIdleOrAllocated() public view {
        assertEq(vault.totalAssets(), vault.idleAssets() + vault.allocatedAssets());
    }

    /// @dev The allocation cap binds at allocation time. Withdrawals drain the idle buffer and can
    ///      leave the realised ratio above the cap; the vault deliberately does not force a
    ///      deallocation on withdrawal, since an illiquid venue would then block exits.
    function invariant_allocationCapHoldsAtAllocationTime() public view {
        assertFalse(handler.ghostCapBreachedAtAllocation());
    }

    /// @dev The strategy never reports holding more than the vault's total assets.
    function invariant_allocatedNeverExceedsTotal() public view {
        assertLe(vault.allocatedAssets(), vault.totalAssets());
    }

    /// @dev Withdrawals never exceed what was put in plus what the vault earned.
    function invariant_noValueCreation() public view {
        assertLe(handler.ghostWithdrawn(), handler.ghostDeposited() + handler.ghostYield());
    }

    /// @dev Zero shares outstanding means no user claim remains against the vault.
    function invariant_noSharesMeansNoClaim() public view {
        if (vault.totalShares() == 0) {
            assertEq(handler.sumActorShares(), 0);
        }
    }

    /// @dev The vault never leaves a standing allowance to the strategy between calls.
    function invariant_noStandingStrategyApproval() public view {
        assertEq(token.allowance(address(vault), address(strategy)), 0);
    }
}
