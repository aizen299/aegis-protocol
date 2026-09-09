// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";

import {HonkVerifier} from "../../generated/HonkVerifier.sol";
import {Roles} from "../../src/shared/access/Roles.sol";
import {ZkVaultGate} from "../../src/zk/ZkVaultGate.sol";
import {CommitmentTreeFixture} from "../utils/CommitmentTreeFixture.sol";
import {ProofFixture} from "../utils/ProofFixture.sol";
import {ZkHandler} from "./ZkHandler.sol";

/// @dev The invariants in docs/v0.4-zk-plan.md §6 that a contract can hold. Each is vacuous if the
///      handler never reaches the code, so ZkProbeTest drives the same handler deterministically.
contract ZkInvariantTest is CommitmentTreeFixture {
    address internal manager = makeAddr("manager");

    ZkVaultGate internal gate;
    ZkHandler internal handler;

    /// @dev Leaves present before the handler starts: the commitment the fixture's proof needs.
    uint256 internal seededLeaves;

    function setUp() public override {
        super.setUp();

        gate = _deployGateAtAFixedAddress(_deployVerifier(), admin, address(tree));

        vm.prank(admin);
        gate.grantRole(Roles.ZK_GATE_MANAGER_ROLE, manager);
        vm.prank(manager);
        gate.registerAction(ProofFixture.ACTION_ID, "vault-membership");

        handler = new ZkHandler(
            tree,
            gate,
            writer,
            manager,
            ProofFixture.PROOF,
            ProofFixture.ROOT,
            ProofFixture.NULLIFIER_HASH,
            ProofFixture.ACTION_ID
        );

        vm.prank(admin);
        tree.grantRole(Roles.COMMITMENT_WRITER_ROLE, address(handler));

        // The commitment the fixture's proof was built for. Without it the tree never holds the
        // root the proof binds, every execution is refused as an unknown root, and the replay
        // invariants below pass while proving nothing.
        _insert(_commitmentOf(424242));
        seededLeaves = tree.leafCount();

        targetContract(address(handler));

        bytes4[] memory selectors = new bytes4[](6);
        selectors[0] = ZkHandler.insert.selector;
        selectors[1] = ZkHandler.insertDuplicate.selector;
        selectors[2] = ZkHandler.executeValidProof.selector;
        selectors[3] = ZkHandler.executeAgainstAnInventedRoot.selector;
        selectors[4] = ZkHandler.executeAgainstAnUnregisteredAction.selector;
        selectors[5] = ZkHandler.churnActionRegistry.selector;
        targetSelector(FuzzSelector({addr: address(handler), selectors: selectors}));
    }

    /// A nullifier is accepted at most once per chain.
    function invariant_aNullifierIsSpentAtMostOnce() public view {
        assertFalse(handler.ghostDoubleSpend(), "a nullifier was spent twice");
        assertLe(handler.ghostExecuteCount(), 1, "the same proof executed more than once");
    }

    /// A proof against a root the tree never held is rejected.
    function invariant_onlyRootsTheTreeHeldAreAccepted() public view {
        assertFalse(handler.ghostUnknownRootAccepted(), "an invented root was accepted");
    }

    /// The gate is action-specific: an unregistered action never passes.
    function invariant_unregisteredActionsNeverExecute() public view {
        assertFalse(handler.ghostUnregisteredActionAccepted(), "an unregistered action executed");
    }

    /// Registry churn must not un-spend a nullifier, or deregistering becomes a way to replay.
    function invariant_aSpentNullifierStaysSpent() public view {
        assertFalse(handler.ghostSpentBecameUnspent(), "a spent nullifier became unspent");
    }

    /// The commitment set only grows, and every leaf that went in is still there.
    function invariant_theCommitmentSetOnlyGrows() public view {
        uint256 count = handler.insertedCount();
        assertEq(
            tree.leafCount(), seededLeaves + handler.ghostInsertCount(), "leaf count and inserts disagree"
        );

        for (uint256 i = 0; i < count; i++) {
            assertTrue(tree.isCommitmentInserted(handler.insertedAt(i)), "an inserted commitment vanished");
        }
    }

    /// Every root the tree produced within the window is still accepted, and the newest always is.
    function invariant_recentRootsStayKnown() public view {
        assertTrue(tree.isKnownRoot(tree.currentRoot()), "the current root is unknown");

        uint256 produced = handler.producedRootCount();
        uint256 window = tree.rootHistorySize();
        if (produced == 0) return;

        uint256 from = produced > window - 1 ? produced - (window - 1) : 0;
        for (uint256 i = from; i < produced; i++) {
            assertTrue(tree.isKnownRoot(handler.producedRootAt(i)), "a root inside the window was forgotten");
        }
    }

    /// The tree never exceeds its capacity, whatever the handler does.
    function invariant_theTreeNeverExceedsCapacity() public view {
        assertLe(tree.leafCount(), tree.capacity(), "the tree overflowed");
    }
}
