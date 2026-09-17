// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IAccessControl} from "@openzeppelin/contracts/access/IAccessControl.sol";

import {WormholeDispatcher} from "../../src/bridge/WormholeDispatcher.sol";
import {IWormholeDispatcher} from "../../src/bridge/interfaces/IWormholeDispatcher.sol";
import {ITimelock} from "../../src/governance/interfaces/ITimelock.sol";
import {Roles} from "../../src/shared/access/Roles.sol";
import {GovernorFixture} from "../utils/GovernorFixture.sol";

/// The dispatcher and the Timelock's remote branch. docs/v2.0-solana-plan.md §16.
contract WormholeDispatcherTest is GovernorFixture {
    address internal operator = makeAddr("operator");
    bytes32 internal constant PROGRAM = bytes32(uint256(0x50));

    function setUp() public override {
        super.setUp();
        vm.startPrank(admin);
        timelock.grantRole(Roles.TIMELOCK_PROPOSER_ROLE, operator);
        timelock.grantRole(Roles.TIMELOCK_EXECUTOR_ROLE, operator);
        vm.stopPrank();
    }

    function _scheduleRemote(
        uint256 value,
        bytes memory payload
    ) internal returns (uint256 operationId) {
        vm.prank(operator);
        (operationId,) = timelock.schedule(_remoteChain(), PROGRAM, value, payload);
        skip(TIMELOCK_DELAY + 1);
    }

    // --- the message ---

    /// Every field at its §16.3 offset, big-endian, with the payload untouched after them.
    function test_theMessageLayoutIsVersionOne() public view {
        bytes memory payload = hex"deadbeef";
        bytes memory message = dispatcher.encodeMessage(9, _remoteChain(), PROGRAM, 1234, payload);

        assertEq(message.length, 1 + 8 + 8 + 8 + 32 + 32 + payload.length, "length");
        assertEq(uint8(message[0]), 1, "version");
        assertEq(_uint(message, 1, 8), block.chainid, "source chain");
        assertEq(_uint(message, 9, 8), 9, "operation");
        assertEq(_uint(message, 17, 8), _remoteChain(), "target chain");
        assertEq(bytes32(_uint(message, 25, 32)), PROGRAM, "target");
        assertEq(_uint(message, 57, 32), 1234, "value");
        assertEq(_slice(message, 89, payload.length), payload, "payload");
    }

    /// A wider id would be truncated into another chain's or operation's id, so it is refused.
    function test_idsThatDoNotFitEightBytesAreRefused() public {
        uint256 wide = uint256(type(uint64).max) + 1;
        vm.expectRevert(abi.encodeWithSelector(IWormholeDispatcher.DoesNotFit.selector, wide));
        dispatcher.encodeMessage(wide, _remoteChain(), PROGRAM, 0, "");
        vm.expectRevert(abi.encodeWithSelector(IWormholeDispatcher.DoesNotFit.selector, wide));
        dispatcher.encodeMessage(1, wide, PROGRAM, 0, "");
    }

    // --- dispatch through the timelock ---

    function test_aRemoteOperationPublishesItsMessageAtFinality() public {
        bytes memory payload = hex"0102";
        uint256 operationId = _scheduleRemote(55, payload);

        vm.expectEmit(true, true, false, true, address(timelock));
        emit ITimelock.OperationDispatched(operationId, _remoteChain(), 0);
        vm.prank(operator);
        timelock.execute(operationId);

        assertEq(
            wormhole.lastPayload(),
            dispatcher.encodeMessage(operationId, _remoteChain(), PROGRAM, 55, payload)
        );
        assertEq(wormhole.lastConsistency(), 1, "not published at Wormhole's finalized consistency level");
        assertEq(wormhole.lastEmitter(), address(dispatcher));
        assertEq(uint8(timelock.operationOf(operationId).state), uint8(ITimelock.OperationState.DISPATCHED));

        vm.prank(operator);
        vm.expectRevert(
            abi.encodeWithSelector(
                ITimelock.OperationNotScheduled.selector, operationId, ITimelock.OperationState.DISPATCHED
            )
        );
        timelock.execute(operationId);
    }

    /// Exactly Wormhole's fee leaves the treasury; the action's declared value does not. §16.4, §16.6.
    function test_theTimelockPaysExactlyTheFeeAndMovesNoValue() public {
        wormhole.setFee(3 gwei);
        vm.deal(address(timelock), 1 ether);
        uint256 operationId = _scheduleRemote(0.5 ether, "");

        vm.prank(operator);
        timelock.execute(operationId);

        assertEq(address(wormhole).balance, 3 gwei, "the fee did not reach Wormhole");
        assertEq(address(timelock).balance, 1 ether - 3 gwei, "more than the fee left the treasury");
        assertEq(address(dispatcher).balance, 0, "value was stranded in the dispatcher");
    }

    function test_aTreasuryThatCannotPayTheFeeRefusesAndStaysScheduled() public {
        wormhole.setFee(1 ether);
        vm.deal(address(timelock), 1 ether - 1);
        uint256 operationId = _scheduleRemote(0, "");

        vm.prank(operator);
        vm.expectRevert(abi.encodeWithSelector(ITimelock.InsufficientBalance.selector, 1 ether, 1 ether - 1));
        timelock.execute(operationId);
        assertEq(uint8(timelock.operationOf(operationId).state), uint8(ITimelock.OperationState.SCHEDULED));
        assertEq(wormhole.published(), 0);
    }

    function test_anOperationForAChainWithNoRouteRefusesAndStaysScheduled() public {
        vm.prank(admin);
        dispatcher.removeRoute(_remoteChain());
        uint256 operationId = _scheduleRemote(0, "");

        vm.prank(operator);
        vm.expectRevert(abi.encodeWithSelector(IWormholeDispatcher.NoRoute.selector, _remoteChain()));
        timelock.execute(operationId);
        assertEq(uint8(timelock.operationOf(operationId).state), uint8(ITimelock.OperationState.SCHEDULED));
    }

    // --- who may do what ---

    /// Only the timelock dispatches. Anything else could publish a governance message no vote chose.
    function test_onlyTheCallerRoleDispatches() public {
        vm.prank(operator);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector,
                operator,
                Roles.DISPATCHER_CALLER_ROLE
            )
        );
        dispatcher.dispatch(1, _remoteChain(), PROGRAM, 0, "");
    }

    function test_theFeeMustBeExact() public {
        wormhole.setFee(2);
        vm.deal(address(timelock), 10);
        for (uint256 sent = 1; sent <= 3; sent += 2) {
            vm.prank(address(timelock));
            vm.expectRevert(abi.encodeWithSelector(IWormholeDispatcher.WrongFee.selector, sent, 2));
            dispatcher.dispatch{value: sent}(1, _remoteChain(), PROGRAM, 0, "");
        }
    }

    function test_onlyTheAdminSetsRoutesOrTheDispatcher() public {
        vm.prank(operator);
        vm.expectRevert();
        dispatcher.setRoute(99, 5, bytes32(uint256(1)));

        vm.prank(operator);
        vm.expectRevert();
        dispatcher.removeRoute(_remoteChain());

        vm.prank(operator);
        vm.expectRevert();
        timelock.setDispatcher(address(1));
    }

    function test_routesMustBeCompleteAndRemote() public {
        vm.startPrank(admin);
        vm.expectRevert(abi.encodeWithSelector(IWormholeDispatcher.LocalChain.selector, block.chainid));
        dispatcher.setRoute(block.chainid, 5, bytes32(uint256(1)));
        vm.expectRevert(abi.encodeWithSelector(IWormholeDispatcher.InvalidRoute.selector, 99));
        dispatcher.setRoute(99, 0, bytes32(uint256(1)));
        vm.expectRevert(abi.encodeWithSelector(IWormholeDispatcher.InvalidRoute.selector, 99));
        dispatcher.setRoute(99, 5, bytes32(0));
        uint256 wide = uint256(type(uint64).max) + 1;
        vm.expectRevert(abi.encodeWithSelector(IWormholeDispatcher.InvalidRoute.selector, wide));
        dispatcher.setRoute(wide, 5, bytes32(uint256(1)));
        vm.expectRevert(abi.encodeWithSelector(IWormholeDispatcher.NoRoute.selector, 99));
        dispatcher.removeRoute(99);
        vm.stopPrank();
    }

    // --- helpers ---

    function _uint(
        bytes memory data,
        uint256 offset,
        uint256 length
    ) internal pure returns (uint256 out) {
        for (uint256 i = 0; i < length; i++) {
            out = (out << 8) | uint8(data[offset + i]);
        }
    }

    function _slice(
        bytes memory data,
        uint256 offset,
        uint256 length
    ) internal pure returns (bytes memory out) {
        out = new bytes(length);
        for (uint256 i = 0; i < length; i++) {
            out[i] = data[offset + i];
        }
    }
}
