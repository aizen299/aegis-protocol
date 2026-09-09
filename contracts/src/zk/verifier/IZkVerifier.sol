// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

/// @notice Calling interface for the generated UltraHonk verifier.
/// @dev Written here rather than imported from the generated source, which is kept out of the
///      compiled tree. The shape is Barretenberg's, not the Groth16 sketch in docs/zk.md — see
///      docs/v0.4-zk-plan.md §2.10.
///
///      `publicInputs` carries the circuit's five public inputs in declaration order:
///      root, nullifierHash, actionId, chainId, gate. The verifier's own
///      NUMBER_OF_PUBLIC_INPUTS is larger because it counts the pairing point object the proof
///      carries internally; the caller supplies only these five.
interface IZkVerifier {
    function verify(
        bytes calldata proof,
        bytes32[] calldata publicInputs
    ) external view returns (bool);
}
