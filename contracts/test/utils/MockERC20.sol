// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {ERC20} from "@openzeppelin/contracts/token/ERC20/ERC20.sol";

/// @dev Test token with an optional transfer fee, to exercise the vault's fee-on-transfer handling.
contract MockERC20 is ERC20 {
    uint8 private immutable _decimals;
    uint256 public transferFeeBps;

    constructor(
        string memory name_,
        string memory symbol_,
        uint8 decimals_
    ) ERC20(name_, symbol_) {
        _decimals = decimals_;
    }

    function decimals() public view override returns (uint8) {
        return _decimals;
    }

    function mint(
        address to,
        uint256 amount
    ) external {
        _mint(to, amount);
    }

    function setTransferFeeBps(
        uint256 bps
    ) external {
        require(bps <= 10_000, "fee too high");
        transferFeeBps = bps;
    }

    function _update(
        address from,
        address to,
        uint256 value
    ) internal override {
        uint256 fee = (from == address(0) || to == address(0)) ? 0 : (value * transferFeeBps) / 10_000;
        if (fee != 0) {
            super._update(from, address(0xdead), fee);
            value -= fee;
        }
        super._update(from, to, value);
    }
}
