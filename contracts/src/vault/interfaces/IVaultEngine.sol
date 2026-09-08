// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

/// @notice External surface of the v0.1 vault. Events here are the backend indexer's entire
///         source of vault state; changing them is a breaking change for `backend/internal/indexer`.
interface IVaultEngine {
    event Deposited(address indexed user, address indexed asset, uint256 amount, uint256 shares);
    event Withdrawn(address indexed user, address indexed asset, uint256 amount, uint256 shares);
    event StrategyUpdated(address indexed previousStrategy, address indexed newStrategy);
    event AllocatedToStrategy(address indexed strategy, uint256 amount);
    event DeallocatedFromStrategy(address indexed strategy, uint256 amount);
    event DepositCapUpdated(uint256 previousCap, uint256 newCap);
    event MinDepositUpdated(uint256 previousMin, uint256 newMin);
    event MaxStrategyAllocationUpdated(uint256 previousBps, uint256 newBps);
    event WithdrawalsFrozenSet(bool frozen);

    error ZeroAddress();
    error ZeroAmount();
    error DepositBelowMinimum(uint256 amount, uint256 minimum);
    error DepositCapExceeded(uint256 totalAssetsAfter, uint256 cap);
    error InsufficientShares(uint256 held, uint256 requested);
    error InsufficientLiquidity(uint256 available, uint256 requested);
    error WithdrawalsFrozen();
    error StrategyAssetMismatch(address strategyAsset, address vaultAsset);
    error StrategyVaultMismatch(address strategyVault, address self);
    error StrategyStillFunded(uint256 remaining);
    error AllocationCapExceeded(uint256 allocatedAfter, uint256 cap);
    error InvalidBps(uint256 bps);

    function deposit(
        uint256 assets,
        address receiver
    ) external returns (uint256 shares);
    function withdraw(
        uint256 shares,
        address receiver
    ) external returns (uint256 assets);

    function asset() external view returns (address);
    function totalAssets() external view returns (uint256);
    function totalShares() external view returns (uint256);
    function sharesOf(
        address account
    ) external view returns (uint256);
    function convertToShares(
        uint256 assets
    ) external view returns (uint256);
    function convertToAssets(
        uint256 shares
    ) external view returns (uint256);
    function idleAssets() external view returns (uint256);
    function allocatedAssets() external view returns (uint256);
}
