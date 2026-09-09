// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IAccessControl} from "@openzeppelin/contracts/access/IAccessControl.sol";
import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Test} from "forge-std/Test.sol";

import {Timelock} from "../../src/governance/Timelock.sol";
import {ITimelock} from "../../src/governance/interfaces/ITimelock.sol";
import {Roles} from "../../src/shared/access/Roles.sol";
import {GovernedTarget} from "../utils/GovernorFixture.sol";

/// @dev The timelock in isolation: roles held by EOAs rather than a governor, so what is under
///      test is the delay and the queue, not the vote that reached them.
contract TimelockTest is Test {
    uint48 internal constant DELAY = 2 days;

    address internal admin = makeAddr("admin");
    address internal proposer = makeAddr("proposer");
    address internal executor = makeAddr("executor");
    address internal canceller = makeAddr("canceller");
    address internal upgrader = makeAddr("upgrader");
    address internal outsider = makeAddr("outsider");

    Timelock internal timelock;
    GovernedTarget internal target;

    function setUp() public {
        target = new GovernedTarget();
        timelock = Timelock(
            payable(address(
                    new ERC1967Proxy(
                        address(new Timelock()), abi.encodeCall(Timelock.initialize, (admin, DELAY))
                    )
                ))
        );

        vm.startPrank(admin);
        timelock.grantRole(Roles.TIMELOCK_PROPOSER_ROLE, proposer);
        timelock.grantRole(Roles.TIMELOCK_EXECUTOR_ROLE, executor);
        timelock.grantRole(Roles.TIMELOCK_CANCELLER_ROLE, canceller);
        timelock.grantRole(Roles.UPGRADER_ROLE, upgrader);
        vm.stopPrank();

        skip(30 days);
    }

    // --- helpers ---

    function _schedule(
        uint256 newValue
    ) internal returns (uint256 operationId) {
        vm.prank(proposer);
        (operationId,) = timelock.schedule(
            block.chainid,
            bytes32(uint256(uint160(address(target)))),
            0,
            abi.encodeCall(GovernedTarget.setValue, (newValue))
        );
    }

    // --- the delay ---

    function test_executionBeforeTheDelayElapsesReverts() public {
        uint256 operationId = _schedule(42);
        uint48 executableAt = timelock.operationOf(operationId).executableAt;

        skip(DELAY - 1);
        vm.prank(executor);
        vm.expectRevert(
            abi.encodeWithSelector(ITimelock.DelayNotElapsed.selector, executableAt, block.timestamp)
        );
        timelock.execute(operationId);

        assertEq(target.value(), 0, "the action ran before its delay elapsed");
    }

    function test_executionAfterTheDelayRuns() public {
        uint256 operationId = _schedule(42);

        skip(DELAY + 1);
        vm.prank(executor);
        timelock.execute(operationId);

        assertEq(target.value(), 42);
        assertEq(uint8(timelock.operationOf(operationId).state), uint8(ITimelock.OperationState.EXECUTED));
    }

    /// Lowering the delay must not shorten a queue someone is already watching — the guarantee the
    /// timelock exists to give. `executableAt` is stamped at schedule time for exactly this reason.
    function test_loweringTheDelayDoesNotMoveAnAlreadyScheduledOperation() public {
        uint256 operationId = _schedule(42);
        uint48 executableAt = timelock.operationOf(operationId).executableAt;

        vm.prank(admin);
        timelock.setDelay(1);

        assertEq(timelock.operationOf(operationId).executableAt, executableAt, "a queued wait moved");

        skip(2);
        vm.prank(executor);
        vm.expectRevert(
            abi.encodeWithSelector(ITimelock.DelayNotElapsed.selector, executableAt, block.timestamp)
        );
        timelock.execute(operationId);
    }

    function test_delayChangeAppliesToTheNextOperation() public {
        vm.prank(admin);
        timelock.setDelay(1 days);

        uint256 operationId = _schedule(42);
        assertEq(timelock.operationOf(operationId).executableAt, uint48(block.timestamp) + 1 days);
    }

    // --- the queue ---

    /// Two proposals that do exactly the same thing are two operations. Deriving the id from the
    /// action would make the second one a collision with the first.
    function test_identicalActionsAreDistinctOperations() public {
        uint256 first = _schedule(42);
        uint256 second = _schedule(42);

        assertTrue(first != second, "identical actions collided");

        skip(DELAY + 1);
        vm.startPrank(executor);
        timelock.execute(first);
        timelock.execute(second);
        vm.stopPrank();
    }

    function test_cancelledOperationCannotExecute() public {
        uint256 operationId = _schedule(42);

        vm.prank(canceller);
        timelock.cancel(operationId);

        skip(DELAY + 1);
        vm.prank(executor);
        vm.expectRevert(
            abi.encodeWithSelector(
                ITimelock.OperationNotScheduled.selector, operationId, ITimelock.OperationState.CANCELLED
            )
        );
        timelock.execute(operationId);

        assertEq(target.value(), 0);
    }

    function test_operationCannotExecuteTwice() public {
        uint256 operationId = _schedule(42);
        skip(DELAY + 1);

        vm.startPrank(executor);
        timelock.execute(operationId);
        vm.expectRevert(
            abi.encodeWithSelector(
                ITimelock.OperationNotScheduled.selector, operationId, ITimelock.OperationState.EXECUTED
            )
        );
        timelock.execute(operationId);
        vm.stopPrank();
    }

    function test_executedOperationCannotBeCancelled() public {
        uint256 operationId = _schedule(42);
        skip(DELAY + 1);
        vm.prank(executor);
        timelock.execute(operationId);

        vm.prank(canceller);
        vm.expectRevert(
            abi.encodeWithSelector(
                ITimelock.OperationNotScheduled.selector, operationId, ITimelock.OperationState.EXECUTED
            )
        );
        timelock.cancel(operationId);
    }

    function test_unknownOperationReverts() public {
        vm.prank(executor);
        vm.expectRevert(abi.encodeWithSelector(ITimelock.UnknownOperation.selector, uint256(7)));
        timelock.execute(7);
    }

    // --- the §7 constraint ---

    function test_crossChainOperationCannotExecuteLocally() public {
        vm.prank(proposer);
        (uint256 operationId,) = timelock.schedule(
            block.chainid + 1,
            bytes32(uint256(uint160(address(target)))),
            0,
            abi.encodeCall(GovernedTarget.setValue, (42))
        );

        skip(DELAY + 1);
        vm.prank(executor);
        vm.expectRevert(
            abi.encodeWithSelector(ITimelock.CrossChainDispatchUnavailable.selector, block.chainid + 1)
        );
        timelock.execute(operationId);

        assertEq(target.value(), 0, "a remote operation performed a local call");
    }

    function test_targetWithHighBytesSetIsRejected() public {
        bytes32 wide = bytes32(uint256(1) << 200 | uint256(uint160(address(target))));

        vm.prank(proposer);
        (uint256 operationId,) =
            timelock.schedule(block.chainid, wide, 0, abi.encodeCall(GovernedTarget.setValue, (42)));

        skip(DELAY + 1);
        vm.prank(executor);
        vm.expectRevert(abi.encodeWithSelector(ITimelock.TargetNotLocalAddress.selector, wide));
        timelock.execute(operationId);
    }

    // --- value ---

    /// The timelock holds what governance spends, so the value moves from here, not the governor.
    function test_valueBearingOperationSpendsFromTheTimelock() public {
        vm.deal(address(timelock), 5 ether);

        vm.prank(proposer);
        (uint256 operationId,) =
            timelock.schedule(block.chainid, bytes32(uint256(uint160(address(target)))), 1 ether, "");

        skip(DELAY + 1);
        vm.prank(executor);
        timelock.execute(operationId);

        assertEq(target.received(), 1 ether);
        assertEq(address(timelock).balance, 4 ether);
    }

    function test_operationExceedingTheBalanceReverts() public {
        vm.deal(address(timelock), 1 ether);

        vm.prank(proposer);
        (uint256 operationId,) =
            timelock.schedule(block.chainid, bytes32(uint256(uint160(address(target)))), 2 ether, "");

        skip(DELAY + 1);
        vm.prank(executor);
        vm.expectRevert(abi.encodeWithSelector(ITimelock.InsufficientBalance.selector, 2 ether, 1 ether));
        timelock.execute(operationId);
    }

    function test_executionSurfacesARevertingTarget() public {
        vm.prank(proposer);
        (uint256 operationId,) = timelock.schedule(
            block.chainid,
            bytes32(uint256(uint160(address(target)))),
            0,
            abi.encodeCall(GovernedTarget.refuse, ())
        );

        skip(DELAY + 1);
        vm.prank(executor);
        vm.expectRevert(abi.encodeWithSelector(ITimelock.ExecutionReverted.selector, operationId));
        timelock.execute(operationId);
    }

    // --- access control ---

    function test_schedulingRequiresTheProposerRole() public {
        bytes32 localTarget = bytes32(uint256(uint160(address(target))));

        vm.prank(outsider);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector,
                outsider,
                Roles.TIMELOCK_PROPOSER_ROLE
            )
        );
        timelock.schedule(block.chainid, localTarget, 0, "");
    }

    function test_executionRequiresTheExecutorRole() public {
        uint256 operationId = _schedule(42);
        skip(DELAY + 1);

        vm.prank(outsider);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector,
                outsider,
                Roles.TIMELOCK_EXECUTOR_ROLE
            )
        );
        timelock.execute(operationId);
    }

    function test_cancellationRequiresTheCancellerRole() public {
        uint256 operationId = _schedule(42);

        vm.prank(outsider);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector,
                outsider,
                Roles.TIMELOCK_CANCELLER_ROLE
            )
        );
        timelock.cancel(operationId);
    }

    function test_setDelayRequiresTheAdminRole() public {
        vm.prank(outsider);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, outsider, bytes32(0)
            )
        );
        timelock.setDelay(1 days);
    }

    function test_upgradeRequiresTheUpgraderRole() public {
        address next = address(new Timelock());

        vm.prank(outsider);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, outsider, Roles.UPGRADER_ROLE
            )
        );
        timelock.upgradeToAndCall(next, "");

        vm.prank(upgrader);
        timelock.upgradeToAndCall(next, "");
        assertEq(timelock.delay(), DELAY, "state did not survive the upgrade");
    }

    // --- initialization ---

    function test_initializeRejectsAZeroDelay() public {
        Timelock implementation = new Timelock();
        bytes memory initData = abi.encodeCall(Timelock.initialize, (admin, 0));

        vm.expectRevert(ITimelock.ZeroValue.selector);
        new ERC1967Proxy(address(implementation), initData);
    }

    function test_initializeRejectsAZeroAdmin() public {
        Timelock implementation = new Timelock();
        bytes memory initData = abi.encodeCall(Timelock.initialize, (address(0), DELAY));

        vm.expectRevert(ITimelock.ZeroAddress.selector);
        new ERC1967Proxy(address(implementation), initData);
    }

    function test_implementationCannotBeInitialized() public {
        Timelock implementation = new Timelock();

        vm.expectRevert();
        implementation.initialize(admin, DELAY);
    }
}
