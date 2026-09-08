// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IERC20} from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import {SafeERC20} from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";

import {IYieldStrategy} from "../../src/vault/interfaces/IYieldStrategy.sol";

/// @dev A strategy that fails the way real yield venues fail: paused markets revert, buggy
///      accounting overstates holdings. Each failure mode is toggled independently so a test can
///      name exactly which call the vault could not survive.
contract HostileStrategy is IYieldStrategy {
    using SafeERC20 for IERC20;

    address public immutable asset;
    address public immutable vault;

    bool public revertOnWithdraw;
    bool public revertOnTotalAssets;
    bool public revertOnEmergency;
    bool public revertOnDeposit;

    /// @dev Assets the strategy claims beyond what it holds. Mimics broken venue accounting.
    uint256 public overstatement;

    error VenuePaused();
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

    function setRevertOnWithdraw(
        bool value
    ) external {
        revertOnWithdraw = value;
    }

    function setRevertOnTotalAssets(
        bool value
    ) external {
        revertOnTotalAssets = value;
    }

    function setRevertOnEmergency(
        bool value
    ) external {
        revertOnEmergency = value;
    }

    function setRevertOnDeposit(
        bool value
    ) external {
        revertOnDeposit = value;
    }

    function setOverstatement(
        uint256 value
    ) external {
        overstatement = value;
    }

    function heldAssets() public view returns (uint256) {
        return IERC20(asset).balanceOf(address(this));
    }

    function totalAssets() public view returns (uint256) {
        if (revertOnTotalAssets) revert VenuePaused();
        return heldAssets() + overstatement;
    }

    function deposit(
        uint256 amount
    ) external onlyVault {
        if (revertOnDeposit) revert VenuePaused();
        IERC20(asset).safeTransferFrom(vault, address(this), amount);
    }

    function withdraw(
        uint256 amount
    ) external onlyVault returns (uint256 withdrawn) {
        if (revertOnWithdraw) revert VenuePaused();

        uint256 available = heldAssets();
        withdrawn = amount > available ? available : amount;
        if (withdrawn != 0) IERC20(asset).safeTransfer(vault, withdrawn);
    }

    function emergencyWithdrawAll() external onlyVault returns (uint256 withdrawn) {
        if (revertOnEmergency) revert VenuePaused();

        withdrawn = heldAssets();
        if (withdrawn != 0) IERC20(asset).safeTransfer(vault, withdrawn);
    }
}
