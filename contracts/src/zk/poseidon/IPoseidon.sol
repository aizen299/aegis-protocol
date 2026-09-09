// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

// Calling interfaces for the generated Poseidon contracts. These declare an ABI rather than
// implement a primitive, which is why they are written here and PoseidonBytecode.sol is not.

/// @notice Poseidon over one field element. Builds a commitment from a secret.
interface IPoseidonT2 {
    function poseidon(
        uint256[1] calldata input
    ) external pure returns (uint256);
}

/// @notice Poseidon over two field elements. Builds Merkle path nodes and the nullifier.
interface IPoseidonT3 {
    function poseidon(
        uint256[2] calldata input
    ) external pure returns (uint256);
}
