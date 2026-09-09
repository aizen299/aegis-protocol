// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

/// @notice The append-only commitment set a membership proof is checked against (v0.4).
/// @dev Events here are the indexer's entire view of the tree; changing them breaks the mirror the
///      proof service builds.
interface ICommitmentTree {
    /// @dev Carries the leaf index and the resulting root so the indexer can rebuild the tree from
    ///      logs alone, without reading contract state — the rule that made ProposalCreated carry
    ///      its whole action.
    event CommitmentInserted(
        uint256 indexed leafIndex, bytes32 indexed commitment, bytes32 root, uint256 timestamp
    );

    error ZeroAddress();
    error TreeIsFull();
    error DuplicateCommitment(bytes32 commitment);
    error CommitmentNotInField(bytes32 commitment);
    error InvalidRootHistorySize(uint32 size);

    function insert(
        bytes32 commitment
    ) external returns (uint32 leafIndex);

    function currentRoot() external view returns (bytes32);

    function isKnownRoot(
        bytes32 root
    ) external view returns (bool);

    function leafCount() external view returns (uint32);
}
