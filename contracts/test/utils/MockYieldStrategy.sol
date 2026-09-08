// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IERC20} from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import {SafeERC20} from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";

import {IYieldStrategy} from "../../src/vault/interfaces/IYieldStrategy.sol";

/// @dev Holds assets as a flat balance. `simulateYield`/`simulateLoss` move the balance without
///      going through the vault, mimicking a venue that gains or loses value.
contract MockYieldStrategy is IYieldStrategy {
    using SafeERC20 for IERC20;

    address public immutable asset;
    address public immutable vault;

    error NotVault();

    constructor(
        address asset_,
        address vault_
    ) {
        asset = asset_;
        vault = vault_;
    }

    modifier onlyVault() {
        if (msg.sender != vault) revert NotVault();
        _;
    }

    function totalAssets() public view returns (uint256) {
        return IERC20(asset).balanceOf(address(this));
    }

    function deposit(
        uint256 amount
    ) external onlyVault {
        IERC20(asset).safeTransferFrom(vault, address(this), amount);
    }

    function withdraw(
        uint256 amount
    ) external onlyVault returns (uint256 withdrawn) {
        uint256 available = totalAssets();
        withdrawn = amount > available ? available : amount;
        if (withdrawn != 0) IERC20(asset).safeTransfer(vault, withdrawn);
    }

    function emergencyWithdrawAll() external onlyVault returns (uint256 withdrawn) {
        withdrawn = totalAssets();
        if (withdrawn != 0) IERC20(asset).safeTransfer(vault, withdrawn);
    }

    function simulateLoss(
        uint256 amount
    ) external {
        IERC20(asset).safeTransfer(address(0xdead), amount);
    }
}
