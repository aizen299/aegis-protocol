// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {ERC20} from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import {ERC20Permit} from "@openzeppelin/contracts/token/ERC20/extensions/ERC20Permit.sol";
import {ERC20Votes} from "@openzeppelin/contracts/token/ERC20/extensions/ERC20Votes.sol";
import {Nonces} from "@openzeppelin/contracts/utils/Nonces.sol";

/// @title AegisToken (v0.3)
/// @notice Governance token with checkpointed voting power.
/// @dev Deliberately not upgradeable, and the only contract in this protocol that is not. Every
///      other contract here can be upgraded because its logic may need to change; a token's entire
///      value is that its rules cannot. A governance token whose code can change is a token whose
///      supply can change, which makes every quorum and threshold expressed as a fraction of supply
///      meaningless.
///
///      Supply is fixed at deployment. There is no mint function: a distribution or inflation model
///      is a product decision this protocol has not made, and encoding an unmade decision in an
///      immutable contract is worse than leaving it out.
contract AegisToken is ERC20, ERC20Permit, ERC20Votes {
    error ZeroAddress();
    error ZeroSupply();

    /// @param recipient Receives the entire supply. A multisig in any real deployment.
    /// @param initialSupply Total supply, fixed forever.
    constructor(
        address recipient,
        uint256 initialSupply
    ) ERC20("Aegis", "AEGIS") ERC20Permit("Aegis") {
        if (recipient == address(0)) revert ZeroAddress();
        if (initialSupply == 0) revert ZeroSupply();

        _mint(recipient, initialSupply);
    }

    /// @notice Voting checkpoints are keyed by timestamp, not block number.
    /// @dev Arbitrum blocks are ~0.25s and irregular, so a voting period counted in blocks drifts
    ///      against the wall clock it is meant to express. Blocks are also not a comparable unit on
    ///      a non-EVM chain. Same reasoning as the oracle's round duration — see
    ///      docs/v0.2-oracle-plan.md §2.1.
    function clock() public view override returns (uint48) {
        return uint48(block.timestamp);
    }

    /// @notice Declares the clock mode, per ERC-6372, so integrators do not have to guess.
    // solhint-disable-next-line func-name-mixedcase
    function CLOCK_MODE() public pure override returns (string memory) {
        return "mode=timestamp";
    }

    // --- required overrides ---

    function _update(
        address from,
        address to,
        uint256 value
    ) internal override(ERC20, ERC20Votes) {
        super._update(from, to, value);
    }

    function nonces(
        address owner
    ) public view override(ERC20Permit, Nonces) returns (uint256) {
        return super.nonces(owner);
    }
}
