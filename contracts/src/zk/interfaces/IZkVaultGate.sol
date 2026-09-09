// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

/// @notice Nullifier-gated authorization for holders of a commitment in the tree (v0.4).
/// @dev Events here are the indexer's view of private actions. They deliberately reveal only the
///      nullifier, the root, and the action — never which commitment was used.
interface IZkVaultGate {
    event PrivateActionExecuted(
        bytes32 indexed nullifier, bytes32 indexed actionId, bytes32 root, uint256 chainId
    );
    event ActionRegistered(bytes32 indexed actionId, string name);
    event ActionDeregistered(bytes32 indexed actionId);

    error ZeroAddress();
    error UnknownAction(bytes32 actionId);
    error ActionAlreadyRegistered(bytes32 actionId);
    error UnknownRoot(bytes32 root);
    error NullifierAlreadySpent(bytes32 nullifier);
    error InvalidProof();
    error EmptyActionName();
    error NotAFieldElement(bytes32 value);

    function executePrivateAction(
        bytes calldata proof,
        bytes32 root,
        bytes32 nullifier,
        bytes32 actionId
    ) external;

    function isSpent(
        bytes32 nullifier
    ) external view returns (bool);

    function isActionRegistered(
        bytes32 actionId
    ) external view returns (bool);
}
