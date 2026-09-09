// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {CommonBase} from "forge-std/Base.sol";
import {StdCheats} from "forge-std/StdCheats.sol";
import {StdUtils} from "forge-std/StdUtils.sol";

import {CommitmentTree} from "../../src/zk/CommitmentTree.sol";
import {ZkVaultGate} from "../../src/zk/ZkVaultGate.sol";

/// @dev Every action returns early rather than reverting: `fail_on_revert = true` is on. The cases
///      that must be observed failing — a replay, a wrong root, an unregistered action — go through
///      try/catch so the refusal is recorded rather than guarded around.
///
///      Only one valid proof exists (the committed fixture), so the handler exercises it repeatedly
///      and records that it succeeds at most once. That is the property, not a limitation.
contract ZkHandler is CommonBase, StdCheats, StdUtils {
    uint256 internal constant FIELD_SIZE =
        21888242871839275222246405745257275088548364400416034343698204186575808495617;

    CommitmentTree public immutable tree;
    ZkVaultGate public immutable gate;
    address public immutable writer;
    address public immutable manager;

    bytes public proof;
    bytes32 public immutable root;
    bytes32 public immutable nullifier;
    bytes32 public immutable actionId;

    bytes32[] public insertedCommitments;
    bytes32[] public producedRoots;

    uint256 public ghostInsertCount;
    uint256 public ghostExecuteCount;
    uint256 public ghostRefusedCount;

    bool public ghostDoubleSpend;
    bool public ghostUnknownRootAccepted;
    bool public ghostUnregisteredActionAccepted;
    bool public ghostSpentBecameUnspent;

    constructor(
        CommitmentTree tree_,
        ZkVaultGate gate_,
        address writer_,
        address manager_,
        bytes memory proof_,
        bytes32 root_,
        bytes32 nullifier_,
        bytes32 actionId_
    ) {
        tree = tree_;
        gate = gate_;
        writer = writer_;
        manager = manager_;
        proof = proof_;
        root = root_;
        nullifier = nullifier_;
        actionId = actionId_;
    }

    // --- the tree ---

    function insert(
        uint256 seed
    ) external {
        bytes32 commitment = bytes32(uint256(keccak256(abi.encode("leaf", seed))) % FIELD_SIZE);
        if (tree.isCommitmentInserted(commitment)) return;
        if (tree.leafCount() >= tree.capacity()) return;

        vm.prank(writer);
        tree.insert(commitment);

        insertedCommitments.push(commitment);
        producedRoots.push(tree.currentRoot());
        ghostInsertCount += 1;
    }

    /// A duplicate must be refused, not merely unlikely.
    function insertDuplicate(
        uint256 seed
    ) external {
        if (insertedCommitments.length == 0) return;
        bytes32 existing = insertedCommitments[seed % insertedCommitments.length];

        vm.prank(writer);
        try tree.insert(existing) {
            insertedCommitments.push(existing);
        } catch {
            ghostRefusedCount += 1;
        }
    }

    // --- the gate ---

    /// The fixture's proof, attempted over and over. It must succeed at most once.
    function executeValidProof() external {
        bool alreadySpent = gate.isSpent(nullifier);

        try gate.executePrivateAction(proof, root, nullifier, actionId) {
            if (alreadySpent) ghostDoubleSpend = true;
            ghostExecuteCount += 1;
        } catch {
            ghostRefusedCount += 1;
        }
    }

    function executeAgainstAnInventedRoot(
        uint256 seed
    ) external {
        bytes32 invented = bytes32(uint256(keccak256(abi.encode("root", seed))) % FIELD_SIZE);
        if (tree.isKnownRoot(invented)) return;

        try gate.executePrivateAction(proof, invented, nullifier, actionId) {
            ghostUnknownRootAccepted = true;
        } catch {
            ghostRefusedCount += 1;
        }
    }

    function executeAgainstAnUnregisteredAction(
        uint256 seed
    ) external {
        bytes32 other = bytes32(uint256(keccak256(abi.encode("action", seed))) % FIELD_SIZE);
        if (gate.isActionRegistered(other)) return;

        try gate.executePrivateAction(proof, root, nullifier, other) {
            ghostUnregisteredActionAccepted = true;
        } catch {
            ghostRefusedCount += 1;
        }
    }

    /// Deregistering must not un-spend anything: a spent nullifier stays spent across any amount of
    /// registry churn, or the registry becomes a way to replay.
    function churnActionRegistry(
        uint256 seed
    ) external {
        bool spentBefore = gate.isSpent(nullifier);

        if (gate.isActionRegistered(actionId)) {
            vm.prank(manager);
            gate.deregisterAction(actionId);
        } else {
            vm.prank(manager);
            gate.registerAction(actionId, "vault-membership");
        }

        if (spentBefore && !gate.isSpent(nullifier)) ghostSpentBecameUnspent = true;
        seed;
    }

    // --- views ---

    function insertedCount() external view returns (uint256) {
        return insertedCommitments.length;
    }

    function insertedAt(
        uint256 i
    ) external view returns (bytes32) {
        return insertedCommitments[i];
    }

    function producedRootCount() external view returns (uint256) {
        return producedRoots.length;
    }

    function producedRootAt(
        uint256 i
    ) external view returns (bytes32) {
        return producedRoots[i];
    }
}
