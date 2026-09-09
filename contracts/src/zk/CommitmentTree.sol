// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {
    AccessControlUpgradeable
} from "@openzeppelin/contracts-upgradeable/access/AccessControlUpgradeable.sol";
import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import {UUPSUpgradeable} from "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";

import {Roles} from "../shared/access/Roles.sol";
import {ICommitmentTree} from "./interfaces/ICommitmentTree.sol";
import {IPoseidonT3} from "./poseidon/IPoseidon.sol";

/// @title CommitmentTree (v0.4)
/// @notice The append-only set a membership proof is checked against.
/// @dev The tree lives on chain rather than being computed off chain and posted, per
///      docs/v0.4-zk-plan.md §2.2. Whoever controls the root controls who can prove membership: a
///      wrong mirror is a liveness bug because proofs stop verifying, while a wrong posted root
///      forges membership. Gas is the right thing to spend on that difference.
///
///      Storage layout is append-only. Run `make contracts-layout-check` before any upgrade.
contract CommitmentTree is Initializable, UUPSUpgradeable, AccessControlUpgradeable, ICommitmentTree {
    /// @dev The BN254 scalar field. A commitment at or above this wraps when the circuit reads it,
    ///      so the two sides would disagree about the same 32 bytes.
    uint256 internal constant FIELD_SIZE =
        21888242871839275222246405745257275088548364400416034343698204186575808495617;

    /// @dev Must equal TREE_DEPTH in zk/circuits/vault_membership/src/main.nr. A mismatch produces
    ///      a root no proof can ever match, so the two are asserted equal in CommitmentTree.t.sol.
    uint32 public constant TREE_DEPTH = 20;

    /// @dev The empty-leaf preimage. Chosen rather than zero so an uninitialised slot is not a
    ///      valid leaf, and derived from a string rather than hand-picked.
    uint256 internal constant EMPTY_LEAF_SEED = uint256(keccak256("aegis.commitment-tree.empty"));

    // --- storage (append-only) ---
    IPoseidonT3 private _hasher;
    uint32 private _nextLeafIndex;
    uint32 private _currentRootIndex;
    uint32 private _rootHistorySize;
    mapping(uint256 level => bytes32 subtree) private _filledSubtrees;
    mapping(uint256 level => bytes32 zero) private _zeros;
    mapping(uint256 index => bytes32 root) private _roots;
    mapping(bytes32 commitment => bool inserted) private _inserted;

    // slither-disable-next-line unused-state
    uint256[40] private __gap;

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {
        _disableInitializers();
    }

    /// @param rootHistorySize_ How many recent roots stay valid. A proof is built against the root
    ///        the prover read; a deposit landing before it is submitted moves the root. Too small
    ///        and honest proofs fail, too large and an old proof stays valid against a wider set.
    function initialize(
        address admin,
        address hasher_,
        uint32 rootHistorySize_
    ) external initializer {
        if (admin == address(0) || hasher_ == address(0)) revert ZeroAddress();
        if (rootHistorySize_ == 0) revert InvalidRootHistorySize(rootHistorySize_);

        __UUPSUpgradeable_init();
        __AccessControl_init();

        _hasher = IPoseidonT3(hasher_);
        _rootHistorySize = rootHistorySize_;

        // Zero subtrees are computed here rather than hardcoded. Hardcoding them would mean
        // hand-writing values derived from Poseidon, which is the failure this module is built to
        // avoid — and a wrong one is undetectable until a proof fails to verify.
        bytes32 zero = bytes32(EMPTY_LEAF_SEED % FIELD_SIZE);
        for (uint256 level = 0; level < TREE_DEPTH; level++) {
            _zeros[level] = zero;
            _filledSubtrees[level] = zero;
            zero = _hash(zero, zero);
        }

        _zeros[TREE_DEPTH] = zero;
        _roots[0] = zero;

        _grantRole(DEFAULT_ADMIN_ROLE, admin);
    }

    /// @inheritdoc ICommitmentTree
    /// @dev Duplicates are rejected: two identical commitments share a nullifier, so only one of
    ///      them could ever be spent and the second depositor's funds would be unreachable.
    function insert(
        bytes32 commitment
    ) external onlyRole(Roles.COMMITMENT_WRITER_ROLE) returns (uint32 leafIndex) {
        if (uint256(commitment) >= FIELD_SIZE) revert CommitmentNotInField(commitment);
        if (_inserted[commitment]) revert DuplicateCommitment(commitment);

        leafIndex = _nextLeafIndex;
        if (leafIndex >= uint32(1) << TREE_DEPTH) revert TreeIsFull();

        _inserted[commitment] = true;
        _nextLeafIndex = leafIndex + 1;

        bytes32 current = commitment;
        uint32 index = leafIndex;

        for (uint256 level = 0; level < TREE_DEPTH; level++) {
            bytes32 left;
            bytes32 right;

            if (index % 2 == 0) {
                left = current;
                right = _zeros[level];
                // The left child of an incomplete pair is remembered so the sibling, when it
                // arrives, hashes against the real subtree rather than a zero.
                _filledSubtrees[level] = current;
            } else {
                left = _filledSubtrees[level];
                right = current;
            }

            current = _hash(left, right);
            index /= 2;
        }

        uint32 nextRootIndex = (_currentRootIndex + 1) % _rootHistorySize;
        _currentRootIndex = nextRootIndex;
        _roots[nextRootIndex] = current;

        // slither-disable-next-line timestamp
        emit CommitmentInserted(leafIndex, commitment, current, block.timestamp);
    }

    // --- views ---

    /// @inheritdoc ICommitmentTree
    function currentRoot() public view returns (bytes32) {
        return _roots[_currentRootIndex];
    }

    /// @inheritdoc ICommitmentTree
    /// @dev A proof is valid against any root in the window, not only the newest. Without this an
    ///      honest proof fails whenever a deposit lands between reading the tree and submitting.
    function isKnownRoot(
        bytes32 root
    ) external view returns (bool) {
        if (root == bytes32(0)) return false;

        uint32 index = _currentRootIndex;
        for (uint32 i = 0; i < _rootHistorySize; i++) {
            if (_roots[index] == root) return true;
            index = index == 0 ? _rootHistorySize - 1 : index - 1;
        }
        return false;
    }

    /// @inheritdoc ICommitmentTree
    /// @dev Also the anonymity set. docs/v0.4-zk-plan.md §4 records that privacy is bounded by it,
    ///      and this is what lets a caller see how bounded rather than assume.
    function leafCount() external view returns (uint32) {
        return _nextLeafIndex;
    }

    function capacity() external pure returns (uint32) {
        return uint32(1) << TREE_DEPTH;
    }

    function rootHistorySize() external view returns (uint32) {
        return _rootHistorySize;
    }

    function hasher() external view returns (address) {
        return address(_hasher);
    }

    function isCommitmentInserted(
        bytes32 commitment
    ) external view returns (bool) {
        return _inserted[commitment];
    }

    function zeroAt(
        uint256 level
    ) external view returns (bytes32) {
        return _zeros[level];
    }

    // --- internals ---

    /// @dev Poseidon lives in its own generated contract, so hashing a Merkle path is necessarily a
    ///      loop of external calls. The callee is fixed at initialization, pure, and holds no state
    ///      to corrupt; the loop is bounded by TREE_DEPTH. The detector's target is an untrusted
    ///      call that can fail or reenter part-way through a loop, which is not this.
    // slither-disable-next-line calls-loop
    function _hash(
        bytes32 left,
        bytes32 right
    ) private view returns (bytes32) {
        return bytes32(_hasher.poseidon([uint256(left), uint256(right)]));
    }

    function _authorizeUpgrade(
        address
    ) internal override onlyRole(Roles.UPGRADER_ROLE) {}
}
