// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {VaultFixture} from "../utils/VaultFixture.sol";

contract VaultEngineFuzzTest is VaultFixture {
    function testFuzz_deposit_mintsNonZeroShares(
        uint256 amount
    ) public {
        amount = bound(amount, MIN_DEPOSIT, DEPOSIT_CAP);
        uint256 shares = _deposit(alice, amount);

        assertGt(shares, 0);
        assertEq(vault.sharesOf(alice), shares);
        assertEq(vault.totalAssets(), amount);
    }

    function testFuzz_depositWithdraw_roundTripNeverProfits(
        uint256 amount
    ) public {
        amount = bound(amount, MIN_DEPOSIT, DEPOSIT_CAP);
        uint256 shares = _deposit(alice, amount);

        vm.prank(alice);
        uint256 out = vault.withdraw(shares, alice);

        assertLe(out, amount);
        assertApproxEqAbs(out, amount, 1);
    }

    function testFuzz_partialWithdraw_leavesProportionalClaim(
        uint256 amount,
        uint256 pct
    ) public {
        amount = bound(amount, MIN_DEPOSIT * 1e3, DEPOSIT_CAP);
        pct = bound(pct, 1, 99);

        uint256 shares = _deposit(alice, amount);
        uint256 burn = (shares * pct) / 100;

        vm.prank(alice);
        uint256 out = vault.withdraw(burn, alice);

        assertLe(out, (amount * pct) / 100);
        assertEq(vault.sharesOf(alice), shares - burn);
    }

    function testFuzz_twoDepositors_cannotStealFromEachOther(
        uint256 a,
        uint256 b
    ) public {
        a = bound(a, MIN_DEPOSIT * 1e3, DEPOSIT_CAP / 2);
        b = bound(b, MIN_DEPOSIT * 1e3, DEPOSIT_CAP / 2);

        _deposit(alice, a);
        _deposit(bob, b);

        uint256 aliceShares = vault.sharesOf(alice);
        uint256 bobShares = vault.sharesOf(bob);

        vm.prank(alice);
        uint256 aliceOut = vault.withdraw(aliceShares, alice);
        vm.prank(bob);
        uint256 bobOut = vault.withdraw(bobShares, bob);

        assertLe(aliceOut, a);
        assertLe(bobOut, b);
    }

    /// @dev Donation-based share inflation: attacker deposits dust, donates a large amount, then a
    ///      victim deposits. The virtual-shares offset must keep the victim's claim near their input.
    function testFuzz_inflationAttack_victimKeepsValue(
        uint256 donation,
        uint256 victimDeposit
    ) public {
        donation = bound(donation, 1e18, 100_000e18);
        victimDeposit = bound(victimDeposit, 1e18, 100_000e18);

        _deposit(alice, MIN_DEPOSIT);
        token.mint(address(vault), donation);

        _fund(bob, victimDeposit);
        vm.prank(bob);
        uint256 victimShares = vault.deposit(victimDeposit, bob);

        vm.prank(bob);
        uint256 out = vault.withdraw(victimShares, bob);

        assertGe(out, (victimDeposit * 9999) / 10_000);
    }

    function testFuzz_convertRoundTrip_neverInflates(
        uint256 assets
    ) public {
        assets = bound(assets, 1, DEPOSIT_CAP);
        _deposit(alice, 1_000e18);

        uint256 shares = vault.convertToShares(assets);
        assertLe(vault.convertToAssets(shares), assets);
    }

    function testFuzz_allocate_neverExceedsCap(
        uint256 deposited,
        uint256 allocated
    ) public {
        deposited = bound(deposited, MIN_DEPOSIT * 1e3, DEPOSIT_CAP);
        allocated = bound(allocated, 1, deposited);

        _deposit(alice, deposited);
        vm.prank(manager);
        vault.setStrategy(address(strategy));

        uint256 cap = (deposited * MAX_ALLOC_BPS) / 10_000;
        vm.prank(manager);
        if (allocated > cap) {
            vm.expectRevert();
            vault.allocate(allocated);
        } else {
            vault.allocate(allocated);
            assertLe(vault.allocatedAssets(), cap);
        }
    }
}
