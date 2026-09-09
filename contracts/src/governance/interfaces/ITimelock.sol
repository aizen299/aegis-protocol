// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

/// @notice Delayed execution of governance actions (v0.3).
/// @dev The timelock, not the governor, is the account that holds protocol roles. A governor can
///      be replaced without re-granting anything; a governor bug does not immediately mean loss of
///      the protocol, because the delay and the canceller still stand between a vote and a call.
interface ITimelock {
    enum OperationState {
        NONE,
        SCHEDULED,
        EXECUTED,
        CANCELLED
    }

    /// @dev The destination is a (chain, target, payload) triple: 32-byte target, never an address.
    ///      See docs/project-spec.md §7.
    struct Operation {
        uint256 targetChainId;
        bytes32 target;
        uint256 value;
        uint48 executableAt;
        OperationState state;
        bytes payload;
    }

    event OperationScheduled(
        uint256 indexed operationId,
        uint256 indexed targetChainId,
        bytes32 target,
        uint256 value,
        bytes payload,
        uint256 executableAt
    );
    event OperationExecuted(uint256 indexed operationId);
    event OperationCancelled(uint256 indexed operationId);
    event DelayUpdated(uint256 previousValue, uint256 newValue);

    error ZeroAddress();
    error ZeroValue();
    error UnknownOperation(uint256 operationId);
    error OperationNotScheduled(uint256 operationId, OperationState state);
    error DelayNotElapsed(uint256 executableAt, uint256 nowTimestamp);
    error TargetNotLocalAddress(bytes32 target);
    error CrossChainDispatchUnavailable(uint256 targetChainId);
    error ExecutionReverted(uint256 operationId);
    error InsufficientBalance(uint256 required, uint256 available);

    function schedule(
        uint256 targetChainId,
        bytes32 target,
        uint256 value,
        bytes calldata payload
    ) external returns (uint256 operationId, uint48 executableAt);

    function execute(
        uint256 operationId
    ) external;

    function cancel(
        uint256 operationId
    ) external;

    function operationOf(
        uint256 operationId
    ) external view returns (Operation memory);

    function delay() external view returns (uint256);
}
