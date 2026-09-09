// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IAccessControl} from "@openzeppelin/contracts/access/IAccessControl.sol";
import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";

import {Roles} from "../../src/shared/access/Roles.sol";
import {CommitmentTree} from "../../src/zk/CommitmentTree.sol";
import {ICommitmentTree} from "../../src/zk/interfaces/ICommitmentTree.sol";
import {CommitmentTreeFixture} from "../utils/CommitmentTreeFixture.sol";

contract CommitmentTreeTest is CommitmentTreeFixture {
    uint256 internal constant FIELD_SIZE =
        21888242871839275222246405745257275088548364400416034343698204186575808495617;

    // --- the root the circuit will be proved against ---

    /// The incremental insert is an optimisation. Rebuilding the whole tree from every leaf is the
    /// definition it has to match, and a disagreement means proofs verify in the circuit against a
    /// root the contract has never held.
    function test_theIncrementalRootMatchesAFullRebuild() public {
        assertEq(tree.currentRoot(), _naiveRoot(), "empty tree disagrees");

        for (uint256 i = 1; i <= 9; i++) {
            _insert(_commitmentOf(i));
            assertEq(tree.currentRoot(), _naiveRoot(), "roots diverged after an insert");
        }
    }

    /// The path the prover will feed the circuit must reproduce the contract's root, walked the way
    /// the circuit walks it. This is the seam a membership proof lives or dies on.
    function test_aPathFromTheTreeReproducesTheRootTheCircuitWay() public {
        for (uint256 i = 1; i <= 6; i++) {
            _insert(_commitmentOf(i));
        }

        for (uint256 leafIndex = 0; leafIndex < leaves.length; leafIndex++) {
            (bytes32[] memory elements, uint256[] memory indices) = _pathFor(leafIndex);
            assertEq(
                _rootFromPath(leaves[leafIndex], elements, indices),
                tree.currentRoot(),
                "the circuit's path walk does not reach the contract's root"
            );
        }
    }

    /// A path belonging to one leaf must not reproduce the root when walked from another. Without
    /// this the previous test would pass for a hash that ignored its inputs.
    function test_aPathFromAnotherLeafDoesNotReproduceTheRoot() public {
        for (uint256 i = 1; i <= 4; i++) {
            _insert(_commitmentOf(i));
        }

        (bytes32[] memory elements, uint256[] memory indices) = _pathFor(0);
        assertTrue(
            _rootFromPath(leaves[1], elements, indices) != tree.currentRoot(),
            "any leaf reproduces the root, so the path proves nothing"
        );
    }

    /// The depth is shared with zk/circuits/vault_membership/src/main.nr. A mismatch produces a
    /// root no proof can ever match, so `make zk-depth-check` compares the two files directly and
    /// this pins the contract's half of it.
    function test_theTreeDepthIsTwenty() public view {
        assertEq(tree.TREE_DEPTH(), 20, "depth changed; update the circuit and the depth check");
        assertEq(tree.capacity(), 1 << 20);
    }

    /// Each zero is the hash of the empty subtree below it. A wrong one silently changes every
    /// root of an incomplete tree, which is every tree in practice.
    function test_theZeroSubtreesAreConsistent() public view {
        for (uint256 level = 0; level < tree.TREE_DEPTH(); level++) {
            assertEq(
                tree.zeroAt(level + 1),
                _hash(tree.zeroAt(level), tree.zeroAt(level)),
                "zero subtree is not the hash of the level below"
            );
        }
        assertTrue(tree.zeroAt(0) != bytes32(0), "an uninitialised slot would be a valid leaf");
    }

    // --- insertion ---

    function test_insertionIsAppendOnlyAndIndexed() public {
        assertEq(tree.leafCount(), 0);

        for (uint256 i = 1; i <= 5; i++) {
            uint32 index = _insert(_commitmentOf(i));
            assertEq(index, uint32(i - 1), "leaf index did not advance by one");
            assertEq(tree.leafCount(), uint32(i));
        }
    }

    function test_insertionEmitsTheLeafIndexAndResultingRoot() public {
        bytes32 commitment = _commitmentOf(42);

        vm.recordLogs();
        vm.prank(writer);
        tree.insert(commitment);

        // The indexer rebuilds the tree from this event alone, so the root it carries must be the
        // root the contract ends up holding.
        assertEq(tree.leafCount(), 1);
        leaves.push(commitment);
        assertEq(tree.currentRoot(), _naiveRoot());
    }

    /// Two identical commitments share a nullifier, so only one could ever be spent and the second
    /// depositor's position would be permanently unreachable.
    function test_aDuplicateCommitmentIsRejected() public {
        bytes32 commitment = _commitmentOf(7);
        _insert(commitment);

        vm.prank(writer);
        vm.expectRevert(abi.encodeWithSelector(ICommitmentTree.DuplicateCommitment.selector, commitment));
        tree.insert(commitment);
    }

    /// A value at or above the field modulus wraps when the circuit reads it, so the two sides
    /// would disagree about the same 32 bytes.
    function test_aCommitmentOutsideTheFieldIsRejected() public {
        bytes32 tooLarge = bytes32(FIELD_SIZE);

        vm.prank(writer);
        vm.expectRevert(abi.encodeWithSelector(ICommitmentTree.CommitmentNotInField.selector, tooLarge));
        tree.insert(tooLarge);
    }

    function test_theLastLeafInTheFieldIsAccepted() public {
        vm.prank(writer);
        tree.insert(bytes32(FIELD_SIZE - 1));
        assertEq(tree.leafCount(), 1);
    }

    /// Reaching capacity must fail closed rather than wrap the index and overwrite leaf zero.
    function test_aFullTreeIsRejected() public {
        // _hasher, _nextLeafIndex, _currentRootIndex and _rootHistorySize all pack into slot 0
        // (`forge inspect CommitmentTree storage-layout`), so the index is written in place rather
        // than overwriting its neighbours. The assertion below is what catches a layout change:
        // filling the tree honestly would take 2^20 inserts.
        uint32 capacity = tree.capacity();
        uint256 packed = uint256(vm.load(address(tree), bytes32(uint256(0))));
        packed &= ~(uint256(type(uint32).max) << 160);
        packed |= uint256(capacity) << 160;
        vm.store(address(tree), bytes32(uint256(0)), bytes32(packed));

        assertEq(tree.leafCount(), capacity, "failed to place the tree at capacity");
        assertEq(tree.hasher(), address(poseidonT3), "the write clobbered a packed neighbour");
        assertEq(tree.rootHistorySize(), ROOT_HISTORY, "the write clobbered a packed neighbour");

        // Hoisted for the same reason as above: an external call in argument position is the
        // "next call" both cheatcodes are waiting for.
        bytes32 commitment = _commitmentOf(1);

        vm.prank(writer);
        vm.expectRevert(ICommitmentTree.TreeIsFull.selector);
        tree.insert(commitment);
    }

    // --- root history ---

    /// A proof is built against the root the prover read. A deposit landing before it is submitted
    /// moves the root, and without a window every honest proof in flight would fail.
    function test_recentRootsStayValid() public {
        bytes32[] memory seen = new bytes32[](5);

        for (uint256 i = 1; i <= 5; i++) {
            _insert(_commitmentOf(i));
            seen[i - 1] = tree.currentRoot();
        }

        for (uint256 i = 0; i < seen.length; i++) {
            assertTrue(tree.isKnownRoot(seen[i]), "a recent root was forgotten");
        }
    }

    /// The window is bounded on purpose: an unbounded history keeps stale proofs valid against an
    /// ever-wider set.
    function test_rootsFallOutOfTheWindow() public {
        _insert(_commitmentOf(1));
        bytes32 oldest = tree.currentRoot();

        for (uint256 i = 2; i <= ROOT_HISTORY + 2; i++) {
            _insert(_commitmentOf(i));
        }

        assertFalse(tree.isKnownRoot(oldest), "the history window does not evict");
        assertTrue(tree.isKnownRoot(tree.currentRoot()), "the current root is not known");
    }

    function test_anUnknownRootIsRejected() public {
        _insert(_commitmentOf(1));

        assertFalse(tree.isKnownRoot(bytes32(uint256(12345))), "an invented root was accepted");
        assertFalse(tree.isKnownRoot(bytes32(0)), "the zero root was accepted");
    }

    function test_theEmptyTreeRootIsKnown() public view {
        assertTrue(tree.isKnownRoot(tree.currentRoot()), "the empty root is not known");
    }

    // --- access control ---

    function test_onlyTheWriterCanInsert() public {
        // Hoisted: _commitmentOf makes an external call, which would otherwise consume the prank
        // and leave `insert` called by the test contract.
        bytes32 commitment = _commitmentOf(1);

        vm.prank(outsider);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector,
                outsider,
                Roles.COMMITMENT_WRITER_ROLE
            )
        );
        tree.insert(commitment);
    }

    function test_upgradeRequiresTheUpgraderRole() public {
        address next = address(new CommitmentTree());

        vm.prank(outsider);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, outsider, Roles.UPGRADER_ROLE
            )
        );
        tree.upgradeToAndCall(next, "");

        _insert(_commitmentOf(1));
        bytes32 rootBefore = tree.currentRoot();

        vm.prank(upgrader);
        tree.upgradeToAndCall(next, "");
        assertEq(tree.currentRoot(), rootBefore, "the tree did not survive the upgrade");
    }

    // --- initialization ---

    function test_initializeRejectsZeroAddressesAndZeroHistory() public {
        address implementation = address(new CommitmentTree());

        vm.expectRevert(ICommitmentTree.ZeroAddress.selector);
        new ERC1967Proxy(
            implementation,
            abi.encodeCall(CommitmentTree.initialize, (address(0), address(poseidonT3), ROOT_HISTORY))
        );

        vm.expectRevert(ICommitmentTree.ZeroAddress.selector);
        new ERC1967Proxy(
            implementation, abi.encodeCall(CommitmentTree.initialize, (admin, address(0), ROOT_HISTORY))
        );

        vm.expectRevert(abi.encodeWithSelector(ICommitmentTree.InvalidRootHistorySize.selector, uint32(0)));
        new ERC1967Proxy(
            implementation, abi.encodeCall(CommitmentTree.initialize, (admin, address(poseidonT3), 0))
        );
    }

    function test_implementationCannotBeInitialized() public {
        CommitmentTree implementation = new CommitmentTree();

        vm.expectRevert();
        implementation.initialize(admin, address(poseidonT3), ROOT_HISTORY);
    }
}
