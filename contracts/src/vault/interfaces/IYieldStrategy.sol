// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

/// @notice Yield venue adapter. The vault is the only permitted caller of the mutating functions.
interface IYieldStrategy {
    /// @notice Underlying asset the strategy accepts. Must equal the vault's asset.
    function asset() external view returns (address);

    /// @notice Vault authorised to allocate to and deallocate from this strategy.
    function vault() external view returns (address);

    /// @notice Assets currently held by the strategy, including accrued yield.
    function totalAssets() external view returns (uint256);

    /// @notice Pull `amount` of asset from the vault and deploy it.
    function deposit(
        uint256 amount
    ) external;

    /// @notice Return up to `amount` of asset to the vault.
    /// @return withdrawn Assets actually transferred back. May be less than `amount`.
    function withdraw(
        uint256 amount
    ) external returns (uint256 withdrawn);

    /// @notice Unwind the entire position back to the vault.
    /// @return withdrawn Assets actually transferred back.
    function emergencyWithdrawAll() external returns (uint256 withdrawn);
}
