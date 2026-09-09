// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IAccessControl} from "@openzeppelin/contracts/access/IAccessControl.sol";

import {OracleStaking} from "../../src/oracle/OracleStaking.sol";
import {IOracleStaking} from "../../src/oracle/interfaces/IOracleStaking.sol";
import {Roles} from "../../src/shared/access/Roles.sol";
import {OracleFixture} from "../utils/OracleFixture.sol";

contract OracleStakingTest is OracleFixture {
    // --- the reason the unbonding period exists ---

    /// Slashing is decided off-chain after a round settles. If stake were released on request, a
    /// node could submit bad data, watch the round settle, and be gone before the slash landed —
    /// making every penalty in the schedule optional. This is the test that pins that shut.
    function test_unstakeCannotOutrunASlash() public {
        _registerNode(nodeA, MIN_STAKE);

        // Node misbehaves, then immediately tries to leave.
        vm.prank(nodeA);
        staking.requestUnstake(MIN_STAKE);

        vm.prank(nodeA);
        vm.expectRevert(
            abi.encodeWithSelector(
                IOracleStaking.UnbondingNotElapsed.selector, block.timestamp + UNBONDING, block.timestamp
            )
        );
        staking.completeUnstake();

        // The backend catches up while the stake is still locked.
        vm.prank(slasher);
        staking.slash(nodeA, 1, MIN_STAKE / 10, REASON_OUTLIER);

        assertEq(staking.stakeOf(nodeA), MIN_STAKE - MIN_STAKE / 10, "the slash landed");
    }

    /// A slash during unbonding takes precedence: only what survives it is released.
    function test_completeUnstakeReleasesOnlyWhatSurvivesASlash() public {
        _registerNode(nodeA, MIN_STAKE);

        vm.prank(nodeA);
        staking.requestUnstake(MIN_STAKE);

        vm.prank(slasher);
        uint256 slashed = staking.slash(nodeA, 1, MIN_STAKE / 10, REASON_OUTLIER);

        skip(UNBONDING);
        vm.prank(nodeA);
        staking.completeUnstake();

        assertEq(stakeToken.balanceOf(nodeA), MIN_STAKE - slashed, "node cannot withdraw slashed stake");
        assertEq(staking.stakeOf(nodeA), 0);
    }

    /// Requesting an exit stops the node participating straight away, so it cannot keep earning
    /// while winding down.
    function test_requestUnstakeDeactivatesImmediately() public {
        _registerNode(nodeA, MIN_STAKE);
        assertTrue(staking.isActive(nodeA));

        vm.prank(nodeA);
        staking.requestUnstake(MIN_STAKE);

        assertFalse(staking.isActive(nodeA));
        assertEq(staking.activeNodeCount(), 0);
    }

    /// The unbonding period cannot be shortened below the floor, which would reopen the escape.
    function test_unbondingPeriodCannotBeShortenedBelowFloor() public {
        vm.prank(admin);
        vm.expectRevert(
            abi.encodeWithSelector(IOracleStaking.UnbondingPeriodTooShort.selector, 1 hours, 1 days)
        );
        staking.setUnbondingPeriod(1 hours);
    }

    /// Shortening the period is an admin-multisig action, not a manager one, because it is the
    /// lever that would let a node escape a pending penalty.
    function test_unbondingPeriodIsAdminGated() public {
        vm.prank(oracleManager);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, oracleManager, bytes32(0)
            )
        );
        staking.setUnbondingPeriod(14 days);
    }

    function test_completeUnstakeSucceedsAfterUnbonding() public {
        _registerNode(nodeA, MIN_STAKE);

        vm.prank(nodeA);
        staking.requestUnstake(MIN_STAKE);
        skip(UNBONDING);

        vm.prank(nodeA);
        staking.completeUnstake();

        assertEq(stakeToken.balanceOf(nodeA), MIN_STAKE);
        assertEq(staking.stakeOf(nodeA), 0);
    }

    function test_cancelUnstakeRestoresActiveStatus() public {
        _registerNode(nodeA, MIN_STAKE);

        vm.prank(nodeA);
        staking.requestUnstake(MIN_STAKE);
        assertFalse(staking.isActive(nodeA));

        vm.prank(nodeA);
        staking.cancelUnstake();

        assertTrue(staking.isActive(nodeA));
        assertEq(staking.activeNodeCount(), 1);
    }

    // --- one penalty per node per round ---

    /// The executor cannot know whether a transaction it lost track of landed. This guard is what
    /// makes retrying safe: the second attempt reverts rather than taking the stake twice.
    function test_secondSlashForTheSameRoundReverts() public {
        _registerNode(nodeA, MIN_STAKE);

        vm.prank(slasher);
        staking.slash(nodeA, 7, MIN_STAKE / 100, REASON_OUTLIER);

        uint256 afterFirst = staking.stakeOf(nodeA);

        vm.prank(slasher);
        vm.expectRevert(abi.encodeWithSelector(IOracleStaking.AlreadySlashedForRound.selector, nodeA, 7));
        staking.slash(nodeA, 7, MIN_STAKE / 100, REASON_OUTLIER);

        assertEq(staking.stakeOf(nodeA), afterFirst, "a retry moved the stake a second time");
    }

    /// The guard is per round, so a node penalised in one round can still be penalised in the next.
    function test_slashInALaterRoundIsAllowed() public {
        _registerNode(nodeA, MIN_STAKE);

        vm.startPrank(slasher);
        staking.slash(nodeA, 7, MIN_STAKE / 100, REASON_OUTLIER);
        staking.slash(nodeA, 8, MIN_STAKE / 100, REASON_MISSED);
        vm.stopPrank();

        assertTrue(staking.slashedInRound(7, nodeA));
        assertTrue(staking.slashedInRound(8, nodeA));
    }

    /// The guard is per node, so one node's penalty does not shield another's.
    function test_guardIsScopedToTheNode() public {
        _registerNode(nodeA, MIN_STAKE);
        _registerNode(nodeB, MIN_STAKE);

        vm.startPrank(slasher);
        staking.slash(nodeA, 7, MIN_STAKE / 100, REASON_OUTLIER);
        staking.slash(nodeB, 7, MIN_STAKE / 100, REASON_OUTLIER);
        vm.stopPrank();

        assertTrue(staking.slashedInRound(7, nodeB));
    }

    /// Round zero is not a round. Allowing it would give every caller a slot the guard cannot
    /// distinguish, which is an unbounded repeat.
    function test_slashRejectsRoundZero() public {
        _registerNode(nodeA, MIN_STAKE);

        vm.prank(slasher);
        vm.expectRevert(abi.encodeWithSelector(IOracleStaking.InvalidRound.selector, uint256(0)));
        staking.slash(nodeA, 0, MIN_STAKE / 100, REASON_OUTLIER);
    }

    /// A reverted attempt must not consume the round: the node was never penalised for it.
    function test_failedSlashDoesNotConsumeTheRound() public {
        _registerNode(nodeA, MIN_STAKE);

        uint256 cap = (MIN_STAKE * MAX_SLASH_BPS) / 10_000;
        vm.prank(slasher);
        vm.expectRevert();
        staking.slash(nodeA, 7, cap + 1, REASON_OUTLIER);

        assertFalse(staking.slashedInRound(7, nodeA), "a rejected slash marked the round used");

        vm.prank(slasher);
        staking.slash(nodeA, 7, cap, REASON_OUTLIER);
        assertTrue(staking.slashedInRound(7, nodeA));
    }

    function test_slashEmitsTheRound() public {
        _registerNode(nodeA, MIN_STAKE);

        vm.expectEmit(true, true, true, true, address(staking));
        emit IOracleStaking.NodeSlashed(
            nodeA, 7, MIN_STAKE / 100, REASON_OUTLIER, MIN_STAKE - MIN_STAKE / 100
        );

        vm.prank(slasher);
        staking.slash(nodeA, 7, MIN_STAKE / 100, REASON_OUTLIER);
    }

    // --- slash caps ---

    /// SLASHER_ROLE is a hot key. The ceiling is enforced here rather than trusted to the caller.
    function test_slashAboveCapReverts() public {
        _registerNode(nodeA, MIN_STAKE);

        uint256 cap = (MIN_STAKE * MAX_SLASH_BPS) / 10_000;
        vm.prank(slasher);
        vm.expectRevert(abi.encodeWithSelector(IOracleStaking.SlashExceedsCap.selector, cap + 1, cap));
        staking.slash(nodeA, 1, cap + 1, REASON_OUTLIER);
    }

    /// A compromised key cannot drain a node in one transaction; each slash is capped against the
    /// remaining stake, so repeated slashing decays rather than zeroes.
    function test_repeatedSlashingDecaysRatherThanZeroes() public {
        _registerNode(nodeA, MIN_STAKE);

        for (uint256 i = 0; i < 10; i++) {
            uint256 cap = (staking.stakeOf(nodeA) * MAX_SLASH_BPS) / 10_000;
            vm.prank(slasher);
            staking.slash(nodeA, i + 1, cap, REASON_OUTLIER);
        }

        assertGt(staking.stakeOf(nodeA), (MIN_STAKE * 30) / 100, "ten max slashes leave over 30%");
    }

    function test_slashBelowFloorDeactivatesNode() public {
        _registerNode(nodeA, MIN_STAKE);

        // Drive the stake under the floor with repeated capped slashes.
        uint256 round = 1;
        while (staking.stakeOf(nodeA) >= STAKE_FLOOR) {
            uint256 cap = (staking.stakeOf(nodeA) * MAX_SLASH_BPS) / 10_000;
            vm.prank(slasher);
            staking.slash(nodeA, round++, cap, REASON_MISSED);
        }

        assertFalse(staking.isActive(nodeA), "a node under the floor stops participating");
        assertEq(staking.activeNodeCount(), 0);
    }

    function test_slashRequiresSlasherRole() public {
        _registerNode(nodeA, MIN_STAKE);

        vm.prank(outsider);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, outsider, Roles.SLASHER_ROLE
            )
        );
        staking.slash(nodeA, 1, 1e18, REASON_OUTLIER);
    }

    function test_slashUnregisteredNodeReverts() public {
        vm.prank(slasher);
        vm.expectRevert(abi.encodeWithSelector(IOracleStaking.NotRegistered.selector, outsider));
        staking.slash(outsider, 1, 1e18, REASON_OUTLIER);
    }

    // --- registration ---

    function test_registerRecordsStakeAndActivates() public {
        _fundNode(nodeA, MIN_STAKE);

        vm.expectEmit(true, false, false, true, address(staking));
        emit IOracleStaking.NodeRegistered(nodeA, MIN_STAKE);

        vm.prank(nodeA);
        staking.register(MIN_STAKE);

        assertEq(staking.stakeOf(nodeA), MIN_STAKE);
        assertTrue(staking.isActive(nodeA));
        assertEq(staking.activeNodeCount(), 1);
        assertEq(stakeToken.balanceOf(address(staking)), MIN_STAKE);
    }

    function test_registerBelowMinimumReverts() public {
        _fundNode(nodeA, MIN_STAKE);

        vm.prank(nodeA);
        vm.expectRevert(
            abi.encodeWithSelector(IOracleStaking.StakeBelowMinimum.selector, MIN_STAKE - 1, MIN_STAKE)
        );
        staking.register(MIN_STAKE - 1);
    }

    function test_registerTwiceReverts() public {
        _registerNode(nodeA, MIN_STAKE);

        vm.prank(nodeA);
        vm.expectRevert(abi.encodeWithSelector(IOracleStaking.AlreadyRegistered.selector, nodeA));
        staking.register(MIN_STAKE);
    }

    /// maxNodes bounds the on-chain median's gas, so it has to actually bind.
    function test_registrationStopsAtMaxNodes() public {
        vm.prank(oracleManager);
        staking.setMaxNodes(2);

        _registerNode(nodeA, MIN_STAKE);
        _registerNode(nodeB, MIN_STAKE);

        address third = makeAddr("third");
        _fundNode(third, MIN_STAKE);
        vm.prank(third);
        vm.expectRevert(abi.encodeWithSelector(IOracleStaking.NodeSetFull.selector, 2));
        staking.register(MIN_STAKE);
    }

    function test_stakeTopUpReactivatesNodeAboveMinimum() public {
        _registerNode(nodeA, MIN_STAKE);

        vm.prank(admin);
        staking.deactivate(nodeA, "MANUAL");
        assertFalse(staking.isActive(nodeA));

        _fundNode(nodeA, MIN_STAKE);
        vm.prank(nodeA);
        staking.stake(1e18);

        assertTrue(staking.isActive(nodeA));
    }

    /// A node winding down must not be reactivated by a top-up; it asked to leave.
    function test_stakeDoesNotReactivateNodeWithPendingUnstake() public {
        _registerNode(nodeA, MIN_STAKE);

        vm.prank(nodeA);
        staking.requestUnstake(MIN_STAKE);

        _fundNode(nodeA, MIN_STAKE);
        vm.prank(nodeA);
        staking.stake(MIN_STAKE);

        assertFalse(staking.isActive(nodeA));
    }

    function test_unregisteredNodeCannotStake() public {
        _fundNode(nodeA, MIN_STAKE);

        vm.prank(nodeA);
        vm.expectRevert(abi.encodeWithSelector(IOracleStaking.NotRegistered.selector, nodeA));
        staking.stake(1e18);
    }

    function test_requestUnstakeAboveStakeReverts() public {
        _registerNode(nodeA, MIN_STAKE);

        vm.prank(nodeA);
        vm.expectRevert(
            abi.encodeWithSelector(IOracleStaking.InsufficientStake.selector, MIN_STAKE, MIN_STAKE + 1)
        );
        staking.requestUnstake(MIN_STAKE + 1);
    }

    function test_secondUnstakeRequestReverts() public {
        _registerNode(nodeA, MIN_STAKE);

        vm.startPrank(nodeA);
        staking.requestUnstake(1e18);
        vm.expectRevert(abi.encodeWithSelector(IOracleStaking.UnstakeAlreadyRequested.selector, nodeA));
        staking.requestUnstake(1e18);
        vm.stopPrank();
    }

    // --- upgrade + init ---

    function test_initializeRejectsShortUnbonding() public {
        OracleStaking impl = new OracleStaking();
        bytes memory data = abi.encodeCall(
            OracleStaking.initialize,
            (admin, address(stakeToken), MIN_STAKE, STAKE_FLOOR, 1 hours, MAX_SLASH_BPS, MAX_NODES)
        );
        vm.expectRevert(
            abi.encodeWithSelector(IOracleStaking.UnbondingPeriodTooShort.selector, 1 hours, 1 days)
        );
        new ERC1967ProxyHarness(address(impl), data);
    }

    function test_upgradeRequiresUpgraderRole() public {
        OracleStaking next = new OracleStaking();
        vm.prank(outsider);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, outsider, Roles.UPGRADER_ROLE
            )
        );
        staking.upgradeToAndCall(address(next), "");
    }
}

/// @dev Thin wrapper so a reverting initializer can be asserted on with `new`.
contract ERC1967ProxyHarness {
    constructor(
        address implementation,
        bytes memory data
    ) {
        (bool ok, bytes memory err) = implementation.delegatecall(data);
        if (!ok) {
            assembly {
                revert(add(err, 0x20), mload(err))
            }
        }
    }
}
