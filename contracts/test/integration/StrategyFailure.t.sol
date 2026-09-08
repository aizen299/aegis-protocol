// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IVaultEngine} from "../../src/vault/interfaces/IVaultEngine.sol";
import {HostileStrategy} from "../utils/HostileStrategy.sol";
import {VaultFixture} from "../utils/VaultFixture.sol";

/// @notice What the vault does when its yield venue misbehaves. A paused market that reverts is the
///         ordinary failure mode for a lending venue, not an exotic one, so these paths decide
///         whether user funds stay reachable.
contract StrategyFailureTest is VaultFixture {
    HostileStrategy internal hostile;

    function setUp() public override {
        super.setUp();
        hostile = new HostileStrategy(address(token), address(vault));
    }

    // --- withdraw() reverting ---

    /// A withdrawal covered by the idle buffer never calls the strategy, so it survives.
    function test_withdrawWithinIdleBuffer_survivesRevertingStrategy() public {
        _deposit(alice, 100e18);
        _allocate(50e18);
        hostile.setRevertOnWithdraw(true);

        uint256 quarter = vault.sharesOf(alice) / 4;
        vm.prank(alice);
        uint256 assets = vault.withdraw(quarter, alice);

        assertApproxEqAbs(assets, 25e18, 1);
        assertEq(token.balanceOf(alice), assets);
    }

    /// A withdrawal that needs the strategy cannot complete while the venue is paused.
    function test_withdrawBeyondIdleBuffer_revertsWhenStrategyReverts() public {
        _deposit(alice, 100e18);
        _allocate(80e18);
        hostile.setRevertOnWithdraw(true);

        uint256 shares = vault.sharesOf(alice);
        vm.prank(alice);
        vm.expectRevert(HostileStrategy.VenuePaused.selector);
        vault.withdraw(shares, alice);
    }

    // --- totalAssets() reverting: the chokepoint ---

    /// totalAssets() is on the path of every deposit and every withdrawal, including withdrawals
    /// the idle buffer could cover on its own. A venue that reverts here halts the whole vault.
    function test_revertingTotalAssets_haltsEveryWithdrawal() public {
        _deposit(alice, 100e18);
        _allocate(50e18);
        hostile.setRevertOnTotalAssets(true);

        uint256 dust = vault.sharesOf(alice) / 100;
        vm.prank(alice);
        vm.expectRevert(HostileStrategy.VenuePaused.selector);
        vault.withdraw(dust, alice);
    }

    function test_revertingTotalAssets_haltsDeposits() public {
        _deposit(alice, 100e18);
        _allocate(50e18);
        hostile.setRevertOnTotalAssets(true);

        _fund(bob, 10e18);
        vm.prank(bob);
        vm.expectRevert(HostileStrategy.VenuePaused.selector);
        vault.deposit(10e18, bob);
    }

    /// The escape hatch is gated on the same call that is broken: setStrategy reads
    /// previous.totalAssets() to check the old venue is unwound.
    function test_revertingTotalAssets_blocksStrategyReplacement() public {
        _deposit(alice, 100e18);
        _allocate(50e18);
        hostile.setRevertOnTotalAssets(true);

        vm.prank(manager);
        vm.expectRevert(HostileStrategy.VenuePaused.selector);
        vault.setStrategy(address(0));
    }

    /// Emergency unwind does not read totalAssets(), so it is the one lever that still works —
    /// unless the venue also reverts on the unwind itself.
    function test_revertingTotalAssets_emergencyUnwindStillWorks() public {
        _deposit(alice, 100e18);
        _allocate(50e18);
        hostile.setRevertOnTotalAssets(true);

        vm.prank(manager);
        uint256 recovered = vault.emergencyDeallocateAll();

        assertEq(recovered, 50e18);
        assertEq(vault.idleAssets(), 100e18);
    }

    /// With both broken, every lever that calls the venue is jammed. Only detachStrategy, which
    /// calls nothing external, still works — see the recovery tests below.
    function test_revertingTotalAssetsAndEmergency_jamsEveryVenueDependentLever() public {
        _deposit(alice, 100e18);
        _allocate(50e18);
        hostile.setRevertOnTotalAssets(true);
        hostile.setRevertOnEmergency(true);

        vm.prank(manager);
        vm.expectRevert(HostileStrategy.VenuePaused.selector);
        vault.emergencyDeallocateAll();

        vm.prank(manager);
        vm.expectRevert(HostileStrategy.VenuePaused.selector);
        vault.setStrategy(address(0));

        uint256 shares = vault.sharesOf(alice);
        vm.prank(alice);
        vm.expectRevert(HostileStrategy.VenuePaused.selector);
        vault.withdraw(shares, alice);
    }

    // --- overstated holdings ---

    /// A venue that overstates its holdings inflates the share price. Early exits are paid at the
    /// inflated rate out of real assets, and the shortfall lands on whoever leaves last.
    function test_overstatedHoldings_payEarlyExitsFromOthersAssets() public {
        _deposit(alice, 100e18);
        _deposit(bob, 100e18);
        _allocate(100e18);

        hostile.setOverstatement(200e18);
        assertEq(vault.totalAssets(), 400e18, "vault trusts the venue's number");

        uint256 aliceShares = vault.sharesOf(alice);
        vm.prank(alice);
        uint256 aliceOut = vault.withdraw(aliceShares, alice);

        assertGt(aliceOut, 100e18, "alice exits at the inflated share price");

        // Bob's claim now exceeds what the vault can actually produce.
        uint256 bobShares = vault.sharesOf(bob);
        uint256 real = vault.idleAssets() + hostile.heldAssets();
        assertLt(real, vault.convertToAssets(bobShares), "bob is left short");
    }

    // --- deposit() reverting ---

    function test_revertingDeposit_blocksAllocationButNotUserFlow() public {
        _deposit(alice, 100e18);

        vm.prank(manager);
        vault.setStrategy(address(hostile));
        hostile.setRevertOnDeposit(true);

        vm.prank(manager);
        vm.expectRevert(HostileStrategy.VenuePaused.selector);
        vault.allocate(50e18);

        // User deposits and withdrawals are untouched: nothing reached the venue.
        uint256 shares = vault.sharesOf(alice);
        vm.prank(alice);
        assertApproxEqAbs(vault.withdraw(shares, alice), 100e18, 1);
    }

    // --- recovery via detachStrategy ---

    /// The jam above is escapable: detaching touches nothing external, so it works precisely when
    /// every other lever is stuck.
    function test_detachStrategy_restoresVaultWhenVenueIsUnreachable() public {
        _deposit(alice, 100e18);
        _allocate(50e18);
        hostile.setRevertOnTotalAssets(true);
        hostile.setRevertOnEmergency(true);
        hostile.setRevertOnWithdraw(true);

        vm.expectEmit(true, false, false, false, address(vault));
        emit IVaultEngine.StrategyDetached(address(hostile));

        vm.prank(admin);
        vault.detachStrategy();

        assertEq(vault.strategy(), address(0));
        assertEq(vault.totalAssets(), 50e18, "the venue's half is written off, not counted");

        uint256 shares = vault.sharesOf(alice);
        vm.prank(alice);
        uint256 assets = vault.withdraw(shares, alice);

        assertApproxEqAbs(assets, 50e18, 1, "alice recovers the idle half");
        assertEq(token.balanceOf(alice), assets);
    }

    /// The write-off lands on everyone at once, so nobody gains by exiting first.
    function test_detachStrategy_lossIsSocialisedAcrossHolders() public {
        _deposit(alice, 100e18);
        _deposit(bob, 100e18);
        _allocate(100e18);
        hostile.setRevertOnTotalAssets(true);

        vm.prank(admin);
        vault.detachStrategy();

        assertApproxEqRel(vault.convertToAssets(vault.sharesOf(alice)), 50e18, 1e14);
        assertApproxEqRel(vault.convertToAssets(vault.sharesOf(bob)), 50e18, 1e14);
    }

    /// Writing off user funds is not routine management, so the manager key cannot do it.
    function test_detachStrategy_deniedToVaultManager() public {
        _deposit(alice, 100e18);
        _allocate(50e18);

        vm.prank(manager);
        vm.expectRevert();
        vault.detachStrategy();
    }

    function test_detachStrategy_revertsWithNoStrategy() public {
        vm.prank(admin);
        vm.expectRevert(IVaultEngine.ZeroAddress.selector);
        vault.detachStrategy();
    }

    /// Detaching does not brick the slot: a replacement can be attached afterwards.
    function test_detachStrategy_allowsReplacementAfterwards() public {
        _deposit(alice, 100e18);
        _allocate(50e18);
        hostile.setRevertOnTotalAssets(true);

        vm.prank(admin);
        vault.detachStrategy();

        vm.prank(manager);
        vault.setStrategy(address(strategy));
        assertEq(vault.strategy(), address(strategy));
    }

    function _allocate(
        uint256 amount
    ) private {
        vm.startPrank(manager);
        vault.setStrategy(address(hostile));
        vault.allocate(amount);
        vm.stopPrank();
    }
}
