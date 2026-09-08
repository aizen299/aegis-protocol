// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

/// @notice Staking and registration surface of the oracle node set (v0.2).
/// @dev Events here are the backend's entire view of node state; changing them breaks
///      `backend/internal/oracle`.
interface IOracleStaking {
    event NodeRegistered(address indexed node, uint256 stake);
    event NodeStaked(address indexed node, uint256 amount, uint256 totalStake);
    event UnstakeRequested(address indexed node, uint256 amount, uint256 claimableAt);
    event UnstakeCancelled(address indexed node, uint256 amount);
    event NodeUnstaked(address indexed node, uint256 amount, uint256 remainingStake);
    event NodeSlashed(address indexed node, uint256 amount, bytes32 indexed reason, uint256 remainingStake);
    event NodeDeactivated(address indexed node, bytes32 indexed reason);
    event NodeReactivated(address indexed node);

    event MinimumStakeUpdated(uint256 previousValue, uint256 newValue);
    event MinStakeFloorUpdated(uint256 previousValue, uint256 newValue);
    event UnbondingPeriodUpdated(uint256 previousValue, uint256 newValue);
    event MaxNodesUpdated(uint256 previousValue, uint256 newValue);

    error ZeroAddress();
    error ZeroAmount();
    error AlreadyRegistered(address node);
    error NotRegistered(address node);
    error StakeBelowMinimum(uint256 provided, uint256 minimum);
    error NodeSetFull(uint256 maxNodes);
    error UnstakeAlreadyRequested(address node);
    error NoUnstakeRequested(address node);
    error UnbondingNotElapsed(uint256 claimableAt, uint256 nowTimestamp);
    error InsufficientStake(uint256 held, uint256 requested);
    error SlashExceedsCap(uint256 requested, uint256 cap);
    error NodeInactive(address node);
    error InvalidBps(uint256 bps);
    error UnbondingPeriodTooShort(uint256 provided, uint256 minimum);

    function register(
        uint256 amount
    ) external;
    function stake(
        uint256 amount
    ) external;
    function requestUnstake(
        uint256 amount
    ) external;
    function cancelUnstake() external;
    function completeUnstake() external;

    function stakeOf(
        address node
    ) external view returns (uint256);
    function isActive(
        address node
    ) external view returns (bool);
    function activeNodeCount() external view returns (uint256);
}
