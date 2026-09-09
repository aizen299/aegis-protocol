// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {
    AccessControlUpgradeable
} from "@openzeppelin/contracts-upgradeable/access/AccessControlUpgradeable.sol";
import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import {UUPSUpgradeable} from "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import {
    ReentrancyGuardUpgradeable
} from "@openzeppelin/contracts-upgradeable/utils/ReentrancyGuardUpgradeable.sol";

import {Roles} from "../shared/access/Roles.sol";
import {ITimelock} from "./interfaces/ITimelock.sol";

/// @title Timelock (v0.3)
/// @notice Holds queued governance actions, enforces the delay, and performs the call.
/// @dev Storage layout is append-only. Run `make contracts-layout-check` before any upgrade.
contract Timelock is
    Initializable,
    UUPSUpgradeable,
    AccessControlUpgradeable,
    ReentrancyGuardUpgradeable,
    ITimelock
{
    // --- storage (append-only) ---
    uint48 private _delay;
    uint256 private _operationCount;
    mapping(uint256 operationId => Operation operation) private _operations;

    // slither-disable-next-line unused-state
    uint256[40] private __gap;

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {
        _disableInitializers();
    }

    function initialize(
        address admin,
        uint48 delay_
    ) external initializer {
        if (admin == address(0)) revert ZeroAddress();
        if (delay_ == 0) revert ZeroValue();

        __UUPSUpgradeable_init();
        __AccessControl_init();
        __ReentrancyGuard_init();

        _delay = delay_;
        _grantRole(DEFAULT_ADMIN_ROLE, admin);
    }

    // --- operations ---

    /// @inheritdoc ITimelock
    /// @dev Ids are assigned here rather than derived from the action, so two proposals that do
    ///      exactly the same thing are two operations instead of a collision.
    function schedule(
        uint256 targetChainId,
        bytes32 target,
        uint256 value,
        bytes calldata payload
    ) external onlyRole(Roles.TIMELOCK_PROPOSER_ROLE) returns (uint256 operationId, uint48 executableAt) {
        executableAt = uint48(block.timestamp) + _delay;

        operationId = ++_operationCount;
        Operation storage operation = _operations[operationId];
        operation.targetChainId = targetChainId;
        operation.target = target;
        operation.value = value;
        operation.executableAt = executableAt;
        operation.state = OperationState.SCHEDULED;
        operation.payload = payload;

        emit OperationScheduled(operationId, targetChainId, target, value, payload, executableAt);
    }

    /// @inheritdoc ITimelock
    /// @dev Branches on the destination chain. The local branch is the direct call it would have
    ///      been anyway; a non-local destination has nowhere to go in Phase 1 and reverts. The
    ///      branch exists so that "local" is not baked into the type — see docs/project-spec.md §7.
    ///      No dispatcher is built here, deliberately.
    function execute(
        uint256 operationId
    ) external onlyRole(Roles.TIMELOCK_EXECUTOR_ROLE) nonReentrant {
        Operation storage operation = _requireOperation(operationId);

        if (operation.state != OperationState.SCHEDULED) {
            revert OperationNotScheduled(operationId, operation.state);
        }
        // slither-disable-next-line timestamp
        if (block.timestamp < operation.executableAt) {
            revert DelayNotElapsed(operation.executableAt, block.timestamp);
        }

        if (operation.targetChainId != block.chainid) {
            revert CrossChainDispatchUnavailable(operation.targetChainId);
        }

        uint256 value = operation.value;
        if (value > address(this).balance) {
            revert InsufficientBalance(value, address(this).balance);
        }

        // Marked executed before the call: an action that reenters must not find itself scheduled.
        operation.state = OperationState.EXECUTED;

        address target = _localAddress(operation.target);

        // An arbitrary call is the mechanism, not an oversight: this contract exists to perform the
        // call a vote chose. A typed interface would restrict governance to a set of functions
        // fixed at deployment, which is the opposite of what it is for. The call is reachable only
        // through a proposer, an elapsed delay, no cancellation, and an executor.
        // slither-disable-next-line low-level-calls
        (bool ok,) = target.call{value: value}(operation.payload);
        if (!ok) revert ExecutionReverted(operationId);

        emit OperationExecuted(operationId);
    }

    /// @inheritdoc ITimelock
    function cancel(
        uint256 operationId
    ) external onlyRole(Roles.TIMELOCK_CANCELLER_ROLE) {
        Operation storage operation = _requireOperation(operationId);

        if (operation.state != OperationState.SCHEDULED) {
            revert OperationNotScheduled(operationId, operation.state);
        }

        operation.state = OperationState.CANCELLED;
        emit OperationCancelled(operationId);
    }

    // --- views ---

    /// @inheritdoc ITimelock
    function operationOf(
        uint256 operationId
    ) external view returns (Operation memory) {
        return _operations[operationId];
    }

    /// @inheritdoc ITimelock
    function delay() external view returns (uint256) {
        return _delay;
    }

    function operationCount() external view returns (uint256) {
        return _operationCount;
    }

    // --- parameters ---

    /// @dev Stamped at schedule time, so a change here never moves an operation that is already
    ///      waiting. Lowering the delay must not shorten a queue someone is watching.
    function setDelay(
        uint48 value
    ) external onlyRole(DEFAULT_ADMIN_ROLE) {
        if (value == 0) revert ZeroValue();
        emit DelayUpdated(_delay, value);
        _delay = value;
    }

    // --- internals ---

    /// @dev Narrows a 32-byte target to a local address, rejecting anything that does not fit. The
    ///      same check types.Identity.EVMAddress performs in the backend.
    function _localAddress(
        bytes32 target
    ) private pure returns (address) {
        if (uint256(target) >> 160 != 0) revert TargetNotLocalAddress(target);
        return address(uint160(uint256(target)));
    }

    function _requireOperation(
        uint256 operationId
    ) private view returns (Operation storage) {
        Operation storage operation = _operations[operationId];
        if (operation.state == OperationState.NONE) revert UnknownOperation(operationId);
        return operation;
    }

    function _authorizeUpgrade(
        address
    ) internal override onlyRole(Roles.UPGRADER_ROLE) {}

    /// @dev Actions may move value, so the treasury this spends from is held here.
    receive() external payable {}
}
