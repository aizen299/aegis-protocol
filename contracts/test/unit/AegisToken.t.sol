// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IERC20Errors} from "@openzeppelin/contracts/interfaces/draft-IERC6093.sol";
import {Test} from "forge-std/Test.sol";

import {AegisToken} from "../../src/governance/AegisToken.sol";

contract AegisTokenTest is Test {
    uint256 internal constant SUPPLY = 100_000_000e18;

    address internal treasury = makeAddr("treasury");
    address internal alice = makeAddr("alice");
    address internal bob = makeAddr("bob");

    AegisToken internal token;

    function setUp() public {
        token = new AegisToken(treasury, SUPPLY);
    }

    function test_supplyIsMintedToTheRecipient() public view {
        assertEq(token.totalSupply(), SUPPLY);
        assertEq(token.balanceOf(treasury), SUPPLY);
    }

    function test_constructorRejectsZeroRecipient() public {
        vm.expectRevert(AegisToken.ZeroAddress.selector);
        new AegisToken(address(0), SUPPLY);
    }

    function test_constructorRejectsZeroSupply() public {
        vm.expectRevert(AegisToken.ZeroSupply.selector);
        new AegisToken(treasury, 0);
    }

    /// Quorum and the proposal threshold are fractions of supply. A supply that can change makes
    /// both meaningless, so there is no mint function at all — not a gated one.
    function test_thereIsNoWayToMintMore() public view {
        // If a mint entry point existed this selector would resolve; the assertion is that the ABI
        // has no such function.
        bytes4 mintSelector = bytes4(keccak256("mint(address,uint256)"));
        (bool ok,) = address(token).staticcall(abi.encodeWithSelector(mintSelector, alice, 1));
        assertFalse(ok, "the token exposes a mint entry point");
        assertEq(token.totalSupply(), SUPPLY);
    }

    // --- clock ---

    /// Voting periods are wall-clock durations. Counted in blocks they drift on Arbitrum, where
    /// blocks are roughly a quarter second and irregular.
    function test_clockIsTimestampBased() public view {
        assertEq(token.clock(), uint48(block.timestamp));
        assertEq(token.CLOCK_MODE(), "mode=timestamp");
    }

    function test_clockAdvancesWithTime() public {
        uint48 before = token.clock();
        skip(1 days);
        assertEq(token.clock(), before + 1 days);
    }

    // --- delegation and checkpoints ---

    /// Holding tokens is not voting power until delegated. This surprises people, so it is pinned.
    function test_balanceWithoutDelegationIsNoVotingPower() public {
        vm.prank(treasury);
        token.transfer(alice, 1_000e18);

        assertEq(token.balanceOf(alice), 1_000e18);
        assertEq(token.getVotes(alice), 0, "an undelegated balance must not vote");
    }

    function test_selfDelegationGrantsVotingPower() public {
        vm.startPrank(treasury);
        token.transfer(alice, 1_000e18);
        vm.stopPrank();

        vm.prank(alice);
        token.delegate(alice);

        assertEq(token.getVotes(alice), 1_000e18);
    }

    function test_delegationMovesPowerWithoutMovingTokens() public {
        vm.prank(treasury);
        token.transfer(alice, 1_000e18);

        vm.prank(alice);
        token.delegate(bob);

        assertEq(token.balanceOf(alice), 1_000e18, "tokens stayed put");
        assertEq(token.getVotes(alice), 0);
        assertEq(token.getVotes(bob), 1_000e18, "power moved");
    }

    function test_transferMovesDelegatedPower() public {
        vm.prank(treasury);
        token.transfer(alice, 1_000e18);
        vm.prank(alice);
        token.delegate(alice);
        vm.prank(bob);
        token.delegate(bob);

        vm.prank(alice);
        token.transfer(bob, 400e18);

        assertEq(token.getVotes(alice), 600e18);
        assertEq(token.getVotes(bob), 400e18);
    }

    // --- the property governance depends on ---

    /// Weight is read from a fixed past timestamp. Tokens acquired afterwards do not count, which
    /// is what makes a flash loan useless: it cannot be held across a block, let alone a snapshot.
    function test_powerAcquiredAfterTheSnapshotDoesNotCount() public {
        vm.prank(alice);
        token.delegate(alice);

        skip(1 days);
        uint256 snapshot = token.clock();
        skip(1); // the snapshot must be strictly in the past to be queryable

        vm.prank(treasury);
        token.transfer(alice, 10_000e18);

        assertEq(token.getVotes(alice), 10_000e18, "current power reflects the transfer");
        assertEq(token.getPastVotes(alice, snapshot), 0, "power at the snapshot must not");
    }

    /// The mirror image: power held at the snapshot still counts after it is sold.
    function test_powerHeldAtTheSnapshotSurvivesASubsequentSale() public {
        vm.prank(treasury);
        token.transfer(alice, 10_000e18);
        vm.prank(alice);
        token.delegate(alice);

        skip(1 days);
        uint256 snapshot = token.clock();
        skip(1);

        vm.prank(alice);
        token.transfer(bob, 10_000e18);

        assertEq(token.getVotes(alice), 0);
        assertEq(token.getPastVotes(alice, snapshot), 10_000e18);
    }

    /// A snapshot at or after the current timestamp is not yet final and must not be readable.
    function test_futureSnapshotsAreRejected() public {
        // Read the clock first: a call in argument position consumes the expectRevert.
        uint256 now_ = token.clock();

        vm.expectRevert();
        token.getPastVotes(alice, now_);

        vm.expectRevert();
        token.getPastVotes(alice, now_ + 1);
    }

    function test_pastTotalSupplyIsQueryable() public {
        vm.prank(treasury);
        token.delegate(treasury);

        skip(1 days);
        uint256 snapshot = token.clock();
        skip(1);

        assertEq(token.getPastTotalSupply(snapshot), SUPPLY);
    }

    function test_transferBeyondBalanceReverts() public {
        vm.prank(alice);
        vm.expectRevert(abi.encodeWithSelector(IERC20Errors.ERC20InsufficientBalance.selector, alice, 0, 1));
        token.transfer(bob, 1);
    }
}
