// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import {PausableUpgradeable} from "@openzeppelin/contracts-upgradeable/utils/PausableUpgradeable.sol";
import {IAccessControl} from "@openzeppelin/contracts/access/IAccessControl.sol";
import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";

import {Roles} from "../../src/shared/access/Roles.sol";
import {VaultEngine} from "../../src/vault/VaultEngine.sol";
import {IVaultEngine} from "../../src/vault/interfaces/IVaultEngine.sol";
import {MockYieldStrategy} from "../utils/MockYieldStrategy.sol";
import {VaultFixture} from "../utils/VaultFixture.sol";

contract VaultEngineTest is VaultFixture {
    // --- initialization ---

    function test_initialize_setsState() public view {
        assertEq(vault.asset(), address(token));
        assertEq(vault.depositCap(), DEPOSIT_CAP);
        assertEq(vault.minDeposit(), MIN_DEPOSIT);
        assertEq(vault.maxStrategyAllocationBps(), MAX_ALLOC_BPS);
        assertTrue(vault.hasRole(vault.DEFAULT_ADMIN_ROLE(), admin));
    }

    function test_initialize_revertsOnSecondCall() public {
        vm.expectRevert(Initializable.InvalidInitialization.selector);
        vault.initialize(admin, address(token), DEPOSIT_CAP, MIN_DEPOSIT, MAX_ALLOC_BPS);
    }

    function test_initialize_revertsOnZeroAdmin() public {
        VaultEngine impl = new VaultEngine();
        bytes memory data = abi.encodeCall(
            VaultEngine.initialize, (address(0), address(token), DEPOSIT_CAP, MIN_DEPOSIT, MAX_ALLOC_BPS)
        );
        vm.expectRevert(IVaultEngine.ZeroAddress.selector);
        new ERC1967Proxy(address(impl), data);
    }

    function test_initialize_revertsOnInvalidBps() public {
        VaultEngine impl = new VaultEngine();
        bytes memory data =
            abi.encodeCall(VaultEngine.initialize, (admin, address(token), DEPOSIT_CAP, MIN_DEPOSIT, 10_001));
        vm.expectRevert(abi.encodeWithSelector(IVaultEngine.InvalidBps.selector, 10_001));
        new ERC1967Proxy(address(impl), data);
    }

    function test_implementation_initializersDisabled() public {
        VaultEngine impl = new VaultEngine();
        vm.expectRevert(Initializable.InvalidInitialization.selector);
        impl.initialize(admin, address(token), DEPOSIT_CAP, MIN_DEPOSIT, MAX_ALLOC_BPS);
    }

    // --- deposit ---

    function test_deposit_mintsSharesAndEmits() public {
        _fund(alice, 100e18);

        vm.expectEmit(true, true, false, true, address(vault));
        emit IVaultEngine.Deposited(alice, address(token), 100e18, 100e18 * 1e3);

        vm.prank(alice);
        uint256 shares = vault.deposit(100e18, alice);

        assertEq(shares, vault.sharesOf(alice));
        assertEq(vault.totalShares(), shares);
        assertEq(vault.totalAssets(), 100e18);
        assertEq(vault.idleAssets(), 100e18);
    }

    function test_deposit_creditsReceiverNotCaller() public {
        _fund(alice, 100e18);
        vm.prank(alice);
        vault.deposit(100e18, bob);

        assertEq(vault.sharesOf(alice), 0);
        assertGt(vault.sharesOf(bob), 0);
    }

    function test_deposit_secondDepositorGetsProportionalShares() public {
        _deposit(alice, 100e18);
        uint256 bobShares = _deposit(bob, 50e18);

        assertApproxEqRel(vault.convertToAssets(bobShares), 50e18, 1e12);
    }

    function test_deposit_revertsBelowMinimum() public {
        _fund(alice, 1e18);
        vm.prank(alice);
        vm.expectRevert(
            abi.encodeWithSelector(IVaultEngine.DepositBelowMinimum.selector, MIN_DEPOSIT - 1, MIN_DEPOSIT)
        );
        vault.deposit(MIN_DEPOSIT - 1, alice);
    }

    function test_deposit_revertsOnZeroAmount() public {
        _fund(alice, 1e18);
        vm.prank(alice);
        vm.expectRevert(IVaultEngine.ZeroAmount.selector);
        vault.deposit(0, alice);
    }

    function test_deposit_revertsOnZeroReceiver() public {
        _fund(alice, 100e18);
        vm.prank(alice);
        vm.expectRevert(IVaultEngine.ZeroAddress.selector);
        vault.deposit(100e18, address(0));
    }

    function test_deposit_revertsAboveCap() public {
        _fund(alice, DEPOSIT_CAP + 1e18);
        vm.prank(alice);
        vm.expectRevert(
            abi.encodeWithSelector(IVaultEngine.DepositCapExceeded.selector, DEPOSIT_CAP + 1, DEPOSIT_CAP)
        );
        vault.deposit(DEPOSIT_CAP + 1, alice);
    }

    function test_deposit_revertsWhenPaused() public {
        vm.prank(pauser);
        vault.pause();

        _fund(alice, 100e18);
        vm.prank(alice);
        vm.expectRevert(PausableUpgradeable.EnforcedPause.selector);
        vault.deposit(100e18, alice);
    }

    function test_deposit_feeOnTransferCreditsReceivedOnly() public {
        token.setTransferFeeBps(100);
        _fund(alice, 100e18);

        vm.prank(alice);
        vault.deposit(100e18, alice);

        assertEq(vault.totalAssets(), 99e18);
        assertEq(vault.convertToAssets(vault.sharesOf(alice)), 99e18);
    }

    // --- withdraw ---

    function test_withdraw_burnsSharesAndPays() public {
        uint256 shares = _deposit(alice, 100e18);

        vm.expectEmit(true, true, false, true, address(vault));
        emit IVaultEngine.Withdrawn(alice, address(token), 100e18, shares);

        vm.prank(alice);
        uint256 assets = vault.withdraw(shares, alice);

        assertEq(assets, 100e18);
        assertEq(token.balanceOf(alice), 100e18);
        assertEq(vault.totalShares(), 0);
        assertEq(vault.sharesOf(alice), 0);
    }

    function test_withdraw_partial() public {
        uint256 shares = _deposit(alice, 100e18);

        vm.prank(alice);
        vault.withdraw(shares / 2, alice);

        assertEq(vault.sharesOf(alice), shares - shares / 2);
        assertApproxEqAbs(vault.totalAssets(), 50e18, 1);
    }

    function test_withdraw_toDifferentReceiver() public {
        uint256 shares = _deposit(alice, 100e18);

        vm.prank(alice);
        vault.withdraw(shares, bob);

        assertEq(token.balanceOf(bob), 100e18);
        assertEq(token.balanceOf(alice), 0);
    }

    function test_withdraw_pullsFromStrategyWhenIdleShort() public {
        _deposit(alice, 100e18);
        _setStrategyAndAllocate(80e18);

        assertEq(vault.idleAssets(), 20e18);

        uint256 shares = vault.sharesOf(alice);
        vm.prank(alice);
        vault.withdraw(shares, alice);

        assertEq(token.balanceOf(alice), 100e18);
        assertEq(vault.allocatedAssets(), 0);
    }

    function test_withdraw_revertsOnInsufficientShares() public {
        uint256 shares = _deposit(alice, 100e18);
        vm.prank(alice);
        vm.expectRevert(abi.encodeWithSelector(IVaultEngine.InsufficientShares.selector, shares, shares + 1));
        vault.withdraw(shares + 1, alice);
    }

    function test_withdraw_revertsOnZeroShares() public {
        _deposit(alice, 100e18);
        vm.prank(alice);
        vm.expectRevert(IVaultEngine.ZeroAmount.selector);
        vault.withdraw(0, alice);
    }

    function test_withdraw_revertsWhenFrozen() public {
        uint256 shares = _deposit(alice, 100e18);

        vm.prank(admin);
        vault.setWithdrawalsFrozen(true);

        vm.prank(alice);
        vm.expectRevert(IVaultEngine.WithdrawalsFrozen.selector);
        vault.withdraw(shares, alice);
    }

    function test_withdraw_openWhilePaused() public {
        uint256 shares = _deposit(alice, 100e18);

        vm.prank(pauser);
        vault.pause();

        vm.prank(alice);
        vault.withdraw(shares, alice);
        assertEq(token.balanceOf(alice), 100e18);
    }

    function test_withdraw_revertsWhenStrategyCannotCoverShortfall() public {
        _deposit(alice, 100e18);
        _setStrategyAndAllocate(80e18);
        strategy.simulateLoss(80e18);

        uint256 shares = vault.sharesOf(alice);
        vm.prank(alice);
        vault.withdraw(shares, alice);

        // Loss is socialised: the holder receives what remains, not the nominal deposit.
        assertEq(token.balanceOf(alice), 20e18);
    }

    // --- yield accrual ---

    function test_yieldIncreasesSharePrice() public {
        uint256 shares = _deposit(alice, 100e18);
        token.mint(address(vault), 10e18);

        assertApproxEqAbs(vault.convertToAssets(shares), 110e18, 1);
    }

    function test_depositAfterYieldMintsFewerShares() public {
        uint256 aliceShares = _deposit(alice, 100e18);
        token.mint(address(vault), 100e18);
        uint256 bobShares = _deposit(bob, 100e18);

        assertLt(bobShares, aliceShares);
        assertApproxEqRel(bobShares, aliceShares / 2, 1e12);
    }

    // --- strategy management ---

    function test_setStrategy_validatesAssetAndVault() public {
        vm.prank(manager);
        vault.setStrategy(address(strategy));
        assertEq(vault.strategy(), address(strategy));
    }

    function test_setStrategy_revertsOnAssetMismatch() public {
        MockYieldStrategy bad = new MockYieldStrategy(address(0xBEEF), address(vault));
        vm.prank(manager);
        vm.expectRevert(
            abi.encodeWithSelector(
                IVaultEngine.StrategyAssetMismatch.selector, address(0xBEEF), address(token)
            )
        );
        vault.setStrategy(address(bad));
    }

    function test_setStrategy_revertsOnVaultMismatch() public {
        MockYieldStrategy bad = new MockYieldStrategy(address(token), address(0xBEEF));
        vm.prank(manager);
        vm.expectRevert(
            abi.encodeWithSelector(
                IVaultEngine.StrategyVaultMismatch.selector, address(0xBEEF), address(vault)
            )
        );
        vault.setStrategy(address(bad));
    }

    function test_setStrategy_revertsWhilePreviousFunded() public {
        _deposit(alice, 100e18);
        _setStrategyAndAllocate(50e18);

        MockYieldStrategy next = new MockYieldStrategy(address(token), address(vault));
        vm.prank(manager);
        vm.expectRevert(abi.encodeWithSelector(IVaultEngine.StrategyStillFunded.selector, 50e18));
        vault.setStrategy(address(next));
    }

    function test_allocate_respectsCap() public {
        _deposit(alice, 100e18);
        vm.prank(manager);
        vault.setStrategy(address(strategy));

        vm.prank(manager);
        vm.expectRevert(abi.encodeWithSelector(IVaultEngine.AllocationCapExceeded.selector, 90e18, 80e18));
        vault.allocate(90e18);
    }

    function test_allocate_revertsWithoutStrategy() public {
        _deposit(alice, 100e18);
        vm.prank(manager);
        vm.expectRevert(IVaultEngine.ZeroAddress.selector);
        vault.allocate(10e18);
    }

    function test_allocate_leavesNoStandingApproval() public {
        _deposit(alice, 100e18);
        _setStrategyAndAllocate(50e18);
        assertEq(token.allowance(address(vault), address(strategy)), 0);
    }

    function test_deallocate_returnsAssetsToIdle() public {
        _deposit(alice, 100e18);
        _setStrategyAndAllocate(80e18);

        vm.prank(manager);
        vault.deallocate(30e18);

        assertEq(vault.idleAssets(), 50e18);
        assertEq(vault.allocatedAssets(), 50e18);
    }

    function test_emergencyDeallocateAll_unwinds() public {
        _deposit(alice, 100e18);
        _setStrategyAndAllocate(80e18);

        vm.prank(manager);
        uint256 withdrawn = vault.emergencyDeallocateAll();

        assertEq(withdrawn, 80e18);
        assertEq(vault.allocatedAssets(), 0);
        assertEq(vault.idleAssets(), 100e18);
    }

    function test_totalAssets_includesStrategyBalance() public {
        _deposit(alice, 100e18);
        _setStrategyAndAllocate(80e18);
        assertEq(vault.totalAssets(), 100e18);
    }

    // --- access control ---

    function test_setStrategy_revertsForNonManager() public {
        vm.prank(alice);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, alice, Roles.VAULT_MANAGER_ROLE
            )
        );
        vault.setStrategy(address(strategy));
    }

    function test_pause_revertsForNonPauser() public {
        vm.prank(alice);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, alice, Roles.PAUSER_ROLE
            )
        );
        vault.pause();
    }

    function test_setWithdrawalsFrozen_revertsForPauser() public {
        vm.prank(pauser);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, pauser, bytes32(0)
            )
        );
        vault.setWithdrawalsFrozen(true);
    }

    function test_setDepositCap_revertsForNonManager() public {
        vm.prank(alice);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, alice, Roles.VAULT_MANAGER_ROLE
            )
        );
        vault.setDepositCap(1);
    }

    // --- risk parameters ---

    function test_setDepositCap_updates() public {
        vm.prank(manager);
        vault.setDepositCap(500e18);
        assertEq(vault.depositCap(), 500e18);
    }

    function test_zeroDepositCap_disablesCap() public {
        vm.prank(manager);
        vault.setDepositCap(0);

        _fund(alice, DEPOSIT_CAP * 2);
        vm.prank(alice);
        vault.deposit(DEPOSIT_CAP * 2, alice);
        assertEq(vault.totalAssets(), DEPOSIT_CAP * 2);
    }

    function test_setMaxStrategyAllocationBps_revertsAbove10000() public {
        vm.prank(manager);
        vm.expectRevert(abi.encodeWithSelector(IVaultEngine.InvalidBps.selector, 10_001));
        vault.setMaxStrategyAllocationBps(10_001);
    }

    // --- upgrade ---

    function test_upgrade_revertsForNonUpgrader() public {
        VaultEngine next = new VaultEngine();
        vm.prank(alice);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, alice, Roles.UPGRADER_ROLE
            )
        );
        vault.upgradeToAndCall(address(next), "");
    }

    function test_upgrade_preservesState() public {
        _deposit(alice, 100e18);
        uint256 sharesBefore = vault.sharesOf(alice);

        VaultEngine next = new VaultEngine();
        vm.prank(upgrader);
        vault.upgradeToAndCall(address(next), "");

        assertEq(vault.sharesOf(alice), sharesBefore);
        assertEq(vault.totalAssets(), 100e18);
        assertEq(vault.asset(), address(token));
    }

    // --- helpers ---

    function _setStrategyAndAllocate(
        uint256 amount
    ) private {
        vm.startPrank(manager);
        vault.setStrategy(address(strategy));
        vault.allocate(amount);
        vm.stopPrank();
    }
}
