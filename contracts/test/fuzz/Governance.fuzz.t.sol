// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IGovernor} from "../../src/governance/interfaces/IGovernor.sol";
import {ITimelock} from "../../src/governance/interfaces/ITimelock.sol";
import {Roles} from "../../src/shared/access/Roles.sol";
import {GovernedTarget, GovernorFixture} from "../utils/GovernorFixture.sol";

contract GovernanceFuzzTest is GovernorFixture {
    address internal proposerEoa = makeAddr("proposerEoa");

    function setUp() public override {
        super.setUp();

        // Timelock properties are tested against the contract directly: routing 512 runs through a
        // full vote would test the governor's clock, not the delay.
        vm.startPrank(admin);
        timelock.grantRole(Roles.TIMELOCK_PROPOSER_ROLE, proposerEoa);
        timelock.grantRole(Roles.TIMELOCK_EXECUTOR_ROLE, proposerEoa);
        vm.stopPrank();
    }

    // --- the §7 target constraint ---

    /// Narrowing is total: a 32-byte value that does not fit an address is refused, and one that
    /// does is never refused *for that reason*. What the resulting call then does is the target's
    /// business — an address holding code that rejects an empty call reverts on its own account,
    /// which is not truncation and must not be confused with it.
    function testFuzz_narrowingRefusesExactlyTheTargetsThatDoNotFit(
        bytes32 rawTarget
    ) public {
        vm.prank(proposerEoa);
        (uint256 operationId,) = timelock.schedule(block.chainid, rawTarget, 0, "");

        skip(TIMELOCK_DELAY + 1);

        if (uint256(rawTarget) >> 160 != 0) {
            vm.prank(proposerEoa);
            vm.expectRevert(abi.encodeWithSelector(ITimelock.TargetNotLocalAddress.selector, rawTarget));
            timelock.execute(operationId);
            return;
        }

        vm.prank(proposerEoa);
        try timelock.execute(operationId) {
            assertEq(uint8(timelock.operationOf(operationId).state), uint8(ITimelock.OperationState.EXECUTED));
        } catch (bytes memory err) {
            assertTrue(
                bytes4(err) != ITimelock.TargetNotLocalAddress.selector,
                "a target that fits an address was refused as too wide"
            );
        }
    }

    /// The positive half, pinned rather than left to the fuzzer: an address that fits is called.
    function testFuzz_aTargetThatFitsIsCalled(
        uint96 low
    ) public {
        // Above the precompile range, and with no code: what is under test is the narrowing and
        // the call, not what some other account chose to do with it.
        address addr = address(uint160(bound(low, 0x100, type(uint96).max)));
        vm.assume(addr.code.length == 0);

        vm.prank(proposerEoa);
        (uint256 operationId,) = timelock.schedule(block.chainid, bytes32(uint256(uint160(addr))), 0, "");

        skip(TIMELOCK_DELAY + 1);
        vm.prank(proposerEoa);
        timelock.execute(operationId);

        assertEq(uint8(timelock.operationOf(operationId).state), uint8(ITimelock.OperationState.EXECUTED));
    }

    /// Any chain that is not this one is refused, whatever its id.
    function testFuzz_onlyTheLocalChainExecutes(
        uint256 chainId
    ) public {
        bytes32 localTarget = bytes32(uint256(uint160(address(target))));

        vm.prank(proposerEoa);
        (uint256 operationId,) =
            timelock.schedule(chainId, localTarget, 0, abi.encodeCall(GovernedTarget.setValue, (42)));

        skip(TIMELOCK_DELAY + 1);

        if (chainId != block.chainid) {
            vm.prank(proposerEoa);
            vm.expectRevert(abi.encodeWithSelector(ITimelock.CrossChainDispatchUnavailable.selector, chainId));
            timelock.execute(operationId);
            assertEq(target.value(), 0, "a remote action landed locally");
            return;
        }

        vm.prank(proposerEoa);
        timelock.execute(operationId);
        assertEq(target.value(), 42);
    }

    // --- the delay ---

    /// The delay is honoured for every combination of delay and wait: executable exactly when the
    /// wait reaches it, never a second earlier.
    function testFuzz_theDelayIsHonouredForEveryWait(
        uint48 delay,
        uint48 wait
    ) public {
        delay = uint48(bound(delay, 1, 365 days));
        wait = uint48(bound(wait, 0, 400 days));

        vm.prank(admin);
        timelock.setDelay(delay);

        bytes32 localTarget = bytes32(uint256(uint160(address(target))));
        vm.prank(proposerEoa);
        (uint256 operationId, uint48 executableAt) =
            timelock.schedule(block.chainid, localTarget, 0, abi.encodeCall(GovernedTarget.setValue, (42)));

        skip(wait);

        if (wait < delay) {
            vm.prank(proposerEoa);
            vm.expectRevert(
                abi.encodeWithSelector(ITimelock.DelayNotElapsed.selector, executableAt, block.timestamp)
            );
            timelock.execute(operationId);
            assertEq(target.value(), 0);
            return;
        }

        vm.prank(proposerEoa);
        timelock.execute(operationId);
        assertEq(target.value(), 42);
    }

    /// Changing the delay never moves an operation already waiting, whatever the new value.
    function testFuzz_aDelayChangeNeverMovesAScheduledOperation(
        uint48 newDelay
    ) public {
        newDelay = uint48(bound(newDelay, 1, 365 days));

        vm.prank(proposerEoa);
        (uint256 operationId, uint48 executableAt) =
            timelock.schedule(block.chainid, bytes32(uint256(uint160(address(target)))), 0, "");

        vm.prank(admin);
        timelock.setDelay(newDelay);

        assertEq(timelock.operationOf(operationId).executableAt, executableAt, "a queued wait moved");
    }

    // --- voting ---

    /// The threshold is a hard gate in both directions: below it nobody proposes, at or above it
    /// everybody does.
    function testFuzz_theProposalThresholdGatesExactly(
        uint256 weight
    ) public {
        weight = bound(weight, 1, SUPPLY / 2);
        _fund(alice, weight);

        if (weight < PROPOSAL_THRESHOLD) {
            vm.prank(alice);
            vm.expectRevert(
                abi.encodeWithSelector(IGovernor.BelowProposalThreshold.selector, weight, PROPOSAL_THRESHOLD)
            );
            governor.propose(_localAction(42), "Fuzz", "");
            return;
        }

        uint256 proposalId = _propose(alice, 42);
        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.PENDING));
    }

    /// Quorum is a fraction of the snapshot supply and can never exceed it, for every numerator
    /// the setter accepts.
    function testFuzz_quorumIsAlwaysAFractionOfSnapshotSupply(
        uint256 numerator
    ) public {
        numerator = bound(numerator, 1, 100);

        vm.prank(admin);
        governor.setQuorumNumerator(numerator);

        uint48 timepoint = uint48(block.timestamp);
        skip(1);

        uint256 supply = token.getPastTotalSupply(timepoint);
        uint256 required = governor.quorum(timepoint);

        assertLe(required, supply, "quorum exceeded the supply backing it");
        assertEq(required, supply * numerator / 100);
    }

    /// A tie is defeated at every magnitude. Passing on a tie would let a proposal with no
    /// majority take effect.
    function testFuzz_aTieIsAlwaysDefeated(
        uint256 amount
    ) public {
        amount = bound(amount, SUPPLY * 3 / 100, SUPPLY * 40 / 100);

        _fund(alice, amount);
        _fund(bob, amount);

        uint256 proposalId = _propose(alice, 42);
        skip(VOTING_DELAY + 1);

        vm.prank(alice);
        governor.castVote(proposalId, uint8(IGovernor.Support.FOR), "");
        vm.prank(bob);
        governor.castVote(proposalId, uint8(IGovernor.Support.AGAINST), "");

        skip(VOTING_PERIOD);
        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.DEFEATED));
    }

    /// Weight acquired after the snapshot never counts, however much of it there is. The
    /// flash-loan property across the whole range.
    function testFuzz_weightAcquiredAfterTheSnapshotNeverCounts(
        uint256 lateAmount
    ) public {
        lateAmount = bound(lateAmount, 1, SUPPLY / 2);

        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        skip(VOTING_DELAY + 1);

        _fund(carol, lateAmount);

        vm.prank(carol);
        vm.expectRevert(abi.encodeWithSelector(IGovernor.NoVotingPower.selector, carol));
        governor.castVote(proposalId, uint8(IGovernor.Support.FOR), "");
    }

    /// Voting is impossible until the snapshot is in the past, and the reported state must say so
    /// rather than advertising a window in which every vote reverts.
    function testFuzz_pendingUntilTheSnapshotIsInThePast(
        uint48 wait
    ) public {
        wait = uint48(bound(wait, 0, VOTING_DELAY));

        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);

        uint256 proposalId = _propose(alice, 42);
        skip(wait);

        assertEq(
            uint8(governor.state(proposalId)),
            uint8(IGovernor.ProposalState.PENDING),
            "active before the snapshot was readable"
        );

        vm.prank(bob);
        vm.expectRevert(
            abi.encodeWithSelector(
                IGovernor.ProposalNotActive.selector, proposalId, IGovernor.ProposalState.PENDING
            )
        );
        governor.castVote(proposalId, uint8(IGovernor.Support.FOR), "");
    }
}
